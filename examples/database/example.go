package database

import (
	"context"
	"fmt"
	"generative-architecture/pkg/conformance"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/dynamicgen"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/process"
	"generative-architecture/pkg/watchgen"
	"go/ast"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strconv"
	"strings"
)

type Example struct {
	Dynamic     dynamicgen.Result
	Resources   dynamicgen.Catalog
	Watches     watchgen.Result
	Program     goflow.Program
	Spec        model.Spec
	Model       *model.Compiled
	Constrained *model.Compiled
}

func Load(root string) (Example, error) {
	source, err := os.ReadFile(filepath.Join(root, "controller.go"))
	if err != nil {
		return Example{}, err
	}
	return LoadSource(root, source)
}

// LoadSource is also used by source-mutation tests; constraints stay unchanged.
func LoadSource(root string, source []byte) (Example, error) {
	wiring, err := os.ReadFile(filepath.Join(root, "wiring.go"))
	if err != nil {
		return Example{}, err
	}
	return LoadSources(root, source, wiring)
}
func LoadSources(root string, source, wiring []byte) (Example, error) {
	watches, err := extractWatches(wiring)
	if err != nil {
		return Example{}, err
	}
	var base model.Spec
	var config process.Config
	if _, err := krm.Read(filepath.Join(root, "model.yaml"), "TransitionModel", &base); err != nil {
		return Example{}, err
	}
	if _, err := krm.Read(filepath.Join(root, "process.yaml"), "ProcessSemantics", &config); err != nil {
		return Example{}, err
	}
	var catalog dynamicgen.Catalog
	if _, err := krm.Read(filepath.Join(root, "resources.yaml"), "KubernetesResourceCatalog", &catalog); err != nil {
		return Example{}, err
	}
	dynamicSource, err := os.ReadFile(filepath.Join(root, "dynamic_store.go"))
	if err != nil {
		return Example{}, err
	}
	accesses, err := dynamicgen.Extract(dynamicSource, "DynamicStore", "s.Client", map[string]string{"DatabaseExists": "readDatabase", "DatabaseInstance": "readInstance", "CreateDatabaseInstance": "createInstance", "UpdateDatabaseStatus": "publish"}, catalog)
	if err != nil {
		return Example{}, err
	}
	if err = accesses.CheckEffects(base.Effects); err != nil {
		return Example{}, err
	}
	registry, err := effect.NewRegistry(base.Effects)
	if err != nil {
		return Example{}, err
	}
	for _, rule := range watches.Rules {
		if _, err = registry.Lookup(rule.Resource, "get", "direct"); err != nil {
			return Example{}, fmt.Errorf("watch identity does not match this slice's API resources: %w", err)
		}
	}
	example, err := Extract(source, base, config, watches)
	if err != nil {
		return Example{}, err
	}
	example.Dynamic = accesses
	example.Resources = catalog
	var requirements struct {
		Properties []model.Property `yaml:"properties"`
	}
	if _, err := krm.Read(filepath.Join(root, "constraints.yaml"), "ModelConstraints", &requirements); err != nil {
		return Example{}, err
	}
	spec := example.Spec
	spec.Properties = requirements.Properties
	example.Constrained, err = model.Compile(spec)
	return example, err
}
func Extract(source []byte, base model.Spec, config process.Config, watches watchgen.Result) (Example, error) {
	scope := map[string]expr.Symbol{}
	for name, v := range base.State {
		scope[name] = expr.Symbol{Type: v.Type, Quint: name}
	}
	opts := goflow.Options{File: "controller.go", Method: "Reconcile", Scope: scope, Returns: []goflow.Return{{Type: "string"}}}
	opts.Statement = func(s ast.Stmt, convert func(ast.Expr, string) (expr.Expression, error)) (goflow.Node, bool, error) {
		operations := map[string]string{
			"found := store.DatabaseExists()":                     "readDatabase",
			"compatible := store.SatisfiesProvisioningContract()": "checkContract",
			"instanceFound, endpoint := store.DatabaseInstance()": "readInstance",
			"store.CreateDatabaseInstance()":                      "createInstance",
		}
		if kind, ok := operations[goflow.Code(s)]; ok {
			return goflow.Node{Kind: kind}, true, nil
		}
		if statement, ok := s.(*ast.ExprStmt); ok {
			if call, ok := statement.X.(*ast.CallExpr); ok && goflow.Code(call.Fun) == "store.UpdateDatabaseStatus" && len(call.Args) == 1 {
				value, err := convert(call.Args[0], "string")
				return goflow.Node{Kind: "publish", Expression: value}, true, err
			}
		}
		return goflow.Node{}, false, nil
	}
	p, err := goflow.Extract(source, opts)
	if err != nil {
		return Example{}, err
	}
	spec, err := process.Expand(base, *p, config)
	if err != nil {
		return Example{}, err
	}
	// Reusable invocation mechanics: the process starts on a reconcile event and
	// returns to the scheduler on every Go return, including a waiting outcome.
	spec.State[config.PC] = model.Variable{Type: "int", Initial: "-1"}
	spec.State[config.Finished] = model.Variable{Type: "bool", Initial: "true"}
	spec.Actions = append(spec.Actions, model.Action{Name: "reconcile", Guard: expr.Expression(config.Finished + " && invocations < 4"), Updates: map[string]expr.Expression{
		config.PC: expr.Expression(strconv.Itoa(p.Entry)), config.Finished: "false", "invocations": "invocations + 1",
		"found": "false", "compatible": "false", "instanceFound": "false", "endpoint": `""`, "outcome": `""`,
	}})
	spec, err = addWatchModel(spec, watches)
	if err != nil {
		return Example{}, err
	}
	compiled, err := model.Compile(spec)
	return Example{Watches: watches, Program: *p, Spec: spec, Model: compiled}, err
}

// Schedule follows the extracted branches. Only environment changes and the
// number of invocations are selected here; controller decisions come from Go.
func (e Example) Schedule(name string) (conformance.Trace, error) {
	state, err := e.Model.Initial()
	if err != nil {
		return conformance.Trace{}, err
	}
	actions := []string{}
	advance := func(name string) error {
		next, err := e.Model.Next(state, name)
		if err != nil {
			return err
		}
		state = next
		actions = append(actions, name)
		return nil
	}
	invoke := func() error {
		if err := advance("reconcile"); err != nil {
			return err
		}
		for steps := 0; state["finished"] != true; steps++ {
			if steps > 64 {
				return fmt.Errorf("process did not return")
			}
			id := state["pc"].(int64)
			if id < 0 || int(id) >= len(e.Program.Nodes) {
				return fmt.Errorf("invalid process counter")
			}
			n := e.Program.Nodes[id]
			if err := advance(fmt.Sprintf("node%d_%s", n.ID, n.Kind)); err != nil {
				return err
			}
		}
		return nil
	}
	switch name {
	case "absent", "pending", "provisioned", "repeated":
		err = advance("discoverCompatible")
	case "unsupported":
		err = advance("discoverUnsupported")
	default:
		return conformance.Trace{}, fmt.Errorf("unknown scenario %s", name)
	}
	if err != nil {
		return conformance.Trace{}, err
	}
	if name == "absent" {
		if err = advance("removeDatabaseBeforeRead"); err != nil {
			return conformance.Trace{}, err
		}
	}
	if err = advance("deliverDatabase"); err != nil {
		return conformance.Trace{}, err
	}
	if err = invoke(); err != nil {
		return conformance.Trace{}, err
	}
	if name == "pending" || name == "provisioned" || name == "repeated" {
		if err = advance("deliverInstance"); err != nil {
			return conformance.Trace{}, err
		}
		if err = invoke(); err != nil {
			return conformance.Trace{}, err
		}
	}
	if name == "provisioned" || name == "repeated" {
		if err = advance("provisionInstance"); err != nil {
			return conformance.Trace{}, err
		}
		if err = advance("deliverInstance"); err != nil {
			return conformance.Trace{}, err
		}
		if err = invoke(); err != nil {
			return conformance.Trace{}, err
		}
	}
	if name == "repeated" {
		if err = advance("deliverDatabase"); err != nil {
			return conformance.Trace{}, err
		}
		if err = invoke(); err != nil {
			return conformance.Trace{}, err
		}
	}
	return conformance.Scenario(e.Model, actions...)
}

// Adapter holds actual fixture contents and observations. It receives events,
// never expected states or branch outcomes. Its Store represents one Database.
type Adapter struct {
	dynamicStore                                      *DynamicStore
	api                                               *dynamicfake.FakeDynamicClient
	watches                                           watchgen.Result
	queue                                             *eventQueue
	databaseEvent, instanceEvent                      string
	instanceUpdatePending                             bool
	ctx                                               context.Context
	cancel                                            context.CancelFunc
	gate                                              *effect.Gate
	worker                                            *cr.Worker
	initErr                                           error
	databaseExists, contractSatisfied, instanceExists bool
	instanceEndpoint, statusEndpoint                  string
	instanceCreates, invocations                      int64
	found, compatible, instanceFound                  bool
	endpoint, outcome                                 string
	running                                           bool
	result                                            chan string
	nodes                                             []goflow.Node
	mutant                                            bool
}

func New(ctx context.Context, e Example, mutant bool) *Adapter {
	ctx, cancel := context.WithCancel(ctx)
	a := &Adapter{watches: e.Watches, ctx: ctx, cancel: cancel, gate: effect.NewGate(), result: make(chan string, 1), nodes: e.Program.Nodes, mutant: mutant}
	if err := a.initDynamic(e); err != nil {
		a.initErr = err
		return a
	}
	a.worker, a.initErr = cr.NewWorker(ctx, reconcile.Request{NamespacedName: databaseKey}, 1, reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
		a.result <- Reconcile(a)
		return reconcile.Result{}, nil
	}))
	a.queue = &eventQueue{worker: a.worker}
	return a
}
func (a *Adapter) Close() {
	a.cancel()
	if a.worker != nil {
		_ = a.worker.Close()
	}
}

func (a *Adapter) read(kind string, values func() map[string]any) map[string]any {
	r := a.gate.Hook(a.ctx, effect.Call{Descriptor: effect.Descriptor{ID: kind}}, func() effect.Result {
		snapshot := values()
		return effect.Result{Snapshot: snapshot, Err: a.dynamicStore.Err}
	})
	if r.Err != nil {
		return nil
	}
	return r.Snapshot
}
func (a *Adapter) DatabaseExists() bool {
	v, _ := a.read("readDatabase", func() map[string]any { return map[string]any{"found": a.dynamicStore.DatabaseExists()} })["found"].(bool)
	return v
}
func (a *Adapter) SatisfiesProvisioningContract() bool {
	v, _ := a.read("checkContract", func() map[string]any {
		return map[string]any{"compatible": a.dynamicStore.SatisfiesProvisioningContract()}
	})["compatible"].(bool)
	return v
}
func (a *Adapter) DatabaseInstance() (bool, string) {
	s := a.read("readInstance", func() map[string]any {
		found, id := a.dynamicStore.DatabaseInstance()
		return map[string]any{"instanceFound": found, "endpoint": id}
	})
	found, _ := s["instanceFound"].(bool)
	id, _ := s["endpoint"].(string)
	return found, id
}
func (a *Adapter) CreateDatabaseInstance() {
	a.gate.Hook(a.ctx, effect.Call{Descriptor: effect.Descriptor{ID: "createInstance"}}, func() effect.Result {
		a.instanceCreates++
		if a.instanceExists {
			return effect.Result{Err: fmt.Errorf("instance already exists")}
		}
		a.dynamicStore.CreateDatabaseInstance()
		if a.dynamicStore.Err != nil {
			return effect.Result{Err: a.dynamicStore.Err}
		}
		a.instanceExists = true
		a.instanceEvent = "create"
		return effect.Result{}
	})
}
func (a *Adapter) UpdateDatabaseStatus(endpoint string) {
	a.gate.Hook(a.ctx, effect.Call{Descriptor: effect.Descriptor{ID: "publish"}, Submitted: map[string]any{"endpoint": endpoint}}, func() effect.Result {
		if a.mutant {
			endpoint = "wrong-endpoint"
		}
		a.dynamicStore.UpdateDatabaseStatus(endpoint)
		if a.dynamicStore.Err != nil {
			return effect.Result{Err: a.dynamicStore.Err}
		}
		a.statusEndpoint = endpoint
		a.databaseEvent = "update"
		return effect.Result{Snapshot: map[string]any{"endpoint": endpoint}}
	})
}
func (a *Adapter) Apply(ctx context.Context, event string) error {
	if a.initErr != nil {
		return a.initErr
	}
	switch event {
	case "discoverCompatible", "discoverUnsupported":
		a.databaseExists = true
		a.contractSatisfied = event == "discoverCompatible"
		a.databaseEvent = "create"
		return a.discoverDynamic()
	case "provisionInstance":
		if !a.instanceExists {
			return fmt.Errorf("no instance to provision")
		}
		if err := a.provisionDynamic(); err != nil {
			return err
		}
		a.instanceEndpoint = "db-1.internal"
		if a.instanceEvent == "create" {
			a.instanceUpdatePending = true
		} else {
			a.instanceEvent = "update"
		}
		return nil
	case "deliverDatabase":
		return a.deliver("Database")
	case "deliverInstance":
		return a.deliver("DatabaseInstance")
	case "removeDatabaseBeforeRead":
		a.databaseExists = false
		a.contractSatisfied = false
		a.databaseEvent = "delete"
		return a.removeDynamic()
	case "idle":
		return nil
	case "reconcile":
		if a.running {
			return fmt.Errorf("invocation still running")
		}
		if a.queue.Len() == 0 {
			return fmt.Errorf("no watch event queued DatabaseReconciler")
		}
		a.dynamicStore.Err = nil
		if err := a.worker.StartOne(); err != nil {
			return err
		}
		a.queue.queued = false
		a.invocations++
		a.found = false
		a.compatible = false
		a.instanceFound = false
		a.endpoint = ""
		a.outcome = ""
		a.running = true
		return nil
	}
	parts := strings.SplitN(strings.TrimPrefix(event, "node"), "_", 2)
	id, err := strconv.Atoi(parts[0])
	if err != nil || id < 0 || id >= len(a.nodes) {
		return fmt.Errorf("unknown process event %s", event)
	}
	node := a.nodes[id]
	switch node.Kind {
	case "branch":
		return nil
	case "return":
		select {
		case a.outcome = <-a.result:
			a.running = false
			if err := a.worker.Wait(); err != nil {
				return err
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	default:
		boundary, err := a.gate.Next(ctx)
		if err != nil {
			return err
		}
		if boundary.Call.Descriptor.ID != node.Kind {
			return fmt.Errorf("expected %s; Go reached %s", node.Kind, boundary.Call.Descriptor.ID)
		}
		r, err := boundary.Complete(ctx, nil)
		if err != nil {
			return err
		}
		if r.Err != nil {
			return r.Err
		}
		switch node.Kind {
		case "readDatabase":
			a.found = r.Snapshot["found"].(bool)
		case "checkContract":
			a.compatible = r.Snapshot["compatible"].(bool)
		case "readInstance":
			a.instanceFound = r.Snapshot["instanceFound"].(bool)
			a.endpoint = r.Snapshot["endpoint"].(string)
		}
	}
	return nil
}
func (a *Adapter) Project(context.Context) (map[string]any, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	expectedReady := 0
	if a.queue.queued && !a.running {
		expectedReady = 1
	}
	if a.queue.Len() != expectedReady {
		return nil, fmt.Errorf("native FIFO queue disagrees with observation bookkeeping")
	}
	apiState, err := a.projectDynamic()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"queued": a.queue.queued, "wrongKey": a.queue.err != nil, "databaseEvent": a.databaseEvent, "instanceEvent": a.instanceEvent, "instanceUpdatePending": a.instanceUpdatePending,
		"databaseExists": apiState["databaseExists"], "contractSatisfied": apiState["contractSatisfied"], "instanceExists": apiState["instanceExists"],
		"instanceEndpoint": apiState["instanceEndpoint"], "statusEndpoint": apiState["statusEndpoint"], "instanceCreates": a.instanceCreates,
		"found": a.found, "compatible": a.compatible, "instanceFound": a.instanceFound, "endpoint": a.endpoint,
		"outcome": a.outcome, "invocations": a.invocations,
	}, nil
}

var Observed = []string{"queued", "wrongKey", "databaseEvent", "instanceEvent", "instanceUpdatePending", "databaseExists", "contractSatisfied", "instanceExists", "instanceEndpoint", "statusEndpoint", "instanceCreates", "found", "compatible", "instanceFound", "endpoint", "outcome", "invocations"}

func (e Example) CheckConstraints(trace conformance.Trace) error {
	for i, s := range trace.States {
		if err := e.Constrained.Check(s.Values, s.Event); err != nil {
			return fmt.Errorf("step %d (%s): %w", i, s.Event, err)
		}
	}
	return nil
}

// FlowLikeC4 draws the extracted control-flow graph rather than a manually
// maintained copy of the four-step story. Return nodes end a reconcile attempt.
func (e Example) FlowLikeC4() string {
	var b strings.Builder
	fmt.Fprintln(&b, "specification { element provisioningStep }\nmodel {")
	for _, n := range e.Program.Nodes {
		fmt.Fprintf(&b, " db_step%d = provisioningStep %q { description %q }\n", n.ID, n.Source, n.Kind)
	}
	for _, n := range e.Program.Nodes {
		if n.Kind == "return" {
			continue
		}
		label := "next"
		if n.Kind == "branch" {
			label = "true"
		}
		fmt.Fprintf(&b, " db_step%d -> db_step%d %q\n", n.ID, n.Next, label)
		if n.Kind == "branch" {
			fmt.Fprintf(&b, " db_step%d -> db_step%d %q\n", n.ID, n.Else, "false")
		}
	}
	names := []string{}
	for _, n := range e.Program.Nodes {
		names = append(names, fmt.Sprintf("db_step%d", n.ID))
	}
	fmt.Fprintf(&b, "}\nviews { view database_provisioning_flow { title %q\n include %s\n } }\n", "Database controller: extracted reconcile flow", strings.Join(names, ", "))
	return b.String()
}

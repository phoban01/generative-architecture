package database

import (
	"context"
	"fmt"

	"github.com/phoban01/generative-architecture/examples/database/generated"
	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/watchgen"
	"github.com/phoban01/generative-architecture/pkg/watchmodel"

	"k8s.io/apimachinery/pkg/api/meta"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Adapter replays the Database controller. It receives events, never
// expected states or branch outcomes. The environment, the boundary
// gating, the Store's gate (StoreProxy), the API projection and the
// reference check are generated (see gen_test.go). The adapter builds the
// fixtures, wires the clients, drives invocations and passes watch events
// through the native handlers (native_delivery.go).
type Adapter struct {
	dynamicStore *DynamicStore
	mapper       meta.RESTMapper
	api          *dynamicfake.FakeDynamicClient
	watches      watchgen.Result
	queue        *eventQueue
	objects      map[string]*watchedObject
	deliveries   map[string]string
	emits        map[string]watchEvent
	observed     []string
	ctx          context.Context
	cancel       context.CancelFunc
	gate         *effect.Gate
	worker       *cr.Worker
	initErr      error
	nodes        map[string]ir.Node
	// results and locals observe Go: the locals Reconcile assigns from
	// Store results, with the zero value of their abstraction type until
	// a call assigns them in the current invocation.
	results map[string][]string
	types   map[string]string
	locals  map[string]any
	// Invocation bookkeeping: Reconcile's result, the reconciles begun and
	// the instance creates attempted.
	outcome                      string
	invocations, instanceCreates int64
	running                      bool
	result                       chan string
}

// New builds the fixtures for one replay of e: an empty fake dynamic API,
// the DynamicStore over an observed client, the generated StoreProxy that
// pauses each Store call at the gate and a controller-runtime worker that
// runs Reconcile through the proxy. A mutant Store writes a wrong endpoint.
func New(ctx context.Context, e Example, mutant bool) *Adapter {
	ctx, cancel := context.WithCancel(ctx)
	a := &Adapter{watches: e.Watches, ctx: ctx, cancel: cancel, gate: effect.NewGate(), result: make(chan string, 1), objects: newWatchedObjects(), deliveries: map[string]string{}, emits: e.emits, observed: e.Observed, nodes: map[string]ir.Node{}, results: e.Results, types: e.Locals, locals: map[string]any{}}
	for _, o := range a.objects {
		a.deliveries[watchmodel.DeliverAction(o.gvk)] = o.gvk.Kind
	}
	for _, n := range e.ReconcileProcess.Nodes {
		a.nodes[n.ID] = n
	}
	a.resetLocals()
	if err := a.initDynamic(e); err != nil {
		a.initErr = err
		return a
	}
	var store Store = a.dynamicStore
	if mutant {
		store = wrongEndpoint{store}
	}
	proxy := StoreProxy{Store: store, Gate: a.gate, Context: ctx}
	a.worker, a.initErr = cr.NewWorker(ctx, reconcile.Request{NamespacedName: databaseKey}, 1, reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
		a.result <- Reconcile(proxy)
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

// wrongEndpoint is a mutant Store that publishes a wrong endpoint.
type wrongEndpoint struct{ Store }

func (s wrongEndpoint) UpdateDatabaseStatus(string) { s.Store.UpdateDatabaseStatus("wrong-endpoint") }

// Apply routes event to the generated environment, the native watch
// delivery, the invocation or the generated process gating.
func (a *Adapter) Apply(ctx context.Context, event string) error {
	if a.initErr != nil {
		return a.initErr
	}
	if ok, err := generated.ApplyEnvironment(ctx, a.api, a.mapper, event); ok {
		if err != nil {
			return err
		}
		a.emit(event)
		return nil
	}
	if kind, ok := a.deliveries[event]; ok {
		return a.deliver(kind)
	}
	switch event {
	case "idle":
		return nil
	case "reconcile":
		return a.begin()
	}
	node, ok := a.nodes[event]
	if !ok {
		return fmt.Errorf("unknown event %s", event)
	}
	if err := generated.ApplyProcessEvent(ctx, a.gate, a.nodes, event, a.observe); err != nil {
		return err
	}
	switch node.Kind {
	case ir.NodeEffect:
		// The Store keeps API errors; replay excludes them.
		if a.dynamicStore.Err != nil {
			return a.dynamicStore.Err
		}
		a.emit(event)
	case ir.NodeReturn:
		select {
		case a.outcome = <-a.result:
			a.running = false
			return a.worker.Wait()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// begin starts an invocation on the key the native queue holds.
func (a *Adapter) begin() error {
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
	a.resetLocals()
	a.outcome = ""
	a.running = true
	return nil
}

// resetLocals sets the Go locals Reconcile assigns from Store results to
// the zero value of their type, as a new invocation sees them.
func (a *Adapter) resetLocals() {
	for _, names := range a.results {
		for _, name := range names {
			if name == "" {
				continue
			}
			switch a.types[name] {
			case "bool":
				a.locals[name] = false
			case "int":
				a.locals[name] = int64(0)
			default:
				a.locals[name] = ""
			}
		}
	}
}

// observe records a completed Store call: its results become the locals
// Reconcile assigns them to, and a create counts.
func (a *Adapter) observe(id string, r effect.Result) {
	effectID := a.nodes[id].Effect
	if effectID == "createInstance" {
		a.instanceCreates++
	}
	for i, name := range a.results[effectID] {
		if v, ok := r.Snapshot[fmt.Sprintf("result%d", i)]; ok && name != "" {
			a.locals[name] = v
		}
	}
}

// emit records the watch event an applied action raises.
func (a *Adapter) emit(event string) {
	if e, ok := a.emits[event]; ok && a.objects[e.kind] != nil {
		a.record(e.kind, e.event, e.generation)
	}
}

// Project returns the observed variables: API state through the generated
// reference check and projection, the watch state, the Go locals and the
// invocation bookkeeping.
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
	apiState, err := a.projectAPI()
	if err != nil {
		return nil, err
	}
	all := map[string]any{"instanceCreates": a.instanceCreates, "outcome": a.outcome, "invocations": a.invocations}
	for name, v := range a.locals {
		all[name] = v
	}
	a.observeWatches(all)
	for name, v := range apiState {
		if _, dup := all[name]; dup {
			return nil, fmt.Errorf("projected %s collides with an observed local", name)
		}
		all[name] = v
	}
	// The model holds no watch state for a resource nobody watches.
	state := make(map[string]any, len(a.observed))
	for _, name := range a.observed {
		v, ok := all[name]
		if !ok {
			return nil, fmt.Errorf("adapter does not observe %s", name)
		}
		state[name] = v
	}
	return state, nil
}

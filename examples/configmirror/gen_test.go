package configmirror

import (
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
)

// The replay adapter's projection, environment and gating are generated
// from the authored inputs: the abstraction document, the reconciler's
// extracted control flow and the effect bindings. This test is the
// generator entry point: run
//
//	go test ./examples/configmirror -run TestAdapterGeneratedFilesUpToDate -regenerate-adapter
//
// to re-emit generated/*_generated.go; without the flag it fails when a
// committed file differs from what the generators produce.
var regenerateAdapter = flag.Bool("regenerate-adapter", false, "rewrite the generated replay adapter files")

// generatedPackage is the package the adapter files are generated into.
const generatedPackage = "generated"

const clientPackage = "sigs.k8s.io/controller-runtime/pkg/client"

// reconcilerKeys bind the Go expressions the reconciler addresses objects by
// to the objects the adapter fixture (newAdapter) supplies: the worker
// reconciles the source's request and the reconciler's Target is the copy.
var reconcilerKeys = map[string]struct{ Namespace, Name string }{
	"req.NamespacedName": {sourceKey.Namespace, sourceKey.Name},
	"r.Target":           {targetKey.Namespace, targetKey.Name},
}

// reconcilerWrites bind the reconciler's field writes to the abstraction:
// setting the copy's data sets its data.value, from the value read from the
// source (the abstraction's snapshot local; see reconcilerValues).
var reconcilerWrites = map[string]string{"target.Data": "ConfigMap_copy_data_value"}

// reconcilerValues bind the Go expressions written by reconcilerWrites.
var reconcilerValues = map[string]expr.Expression{`map[string]string{"value": source.Data["value"]}`: "snapshot"}

// boundaryEvents name the outcomes of the reconciler's effects as the
// handwritten system.yaml names its actions. The model is not generated
// yet, so these names are bound here; TestAdapterBoundaryEventsMatchModel
// keeps them in step with the model's effect-labelled actions.
var boundaryEvents = []adaptergen.BoundaryEvent{
	{Name: "readSource", Effect: "readSource"},
	{Name: "readSourceError", Effect: "readSource", Error: true},
	{Name: "readTarget", Effect: "readTarget"},
	{Name: "readTargetError", Effect: "readTarget", Error: true},
	{Name: "commit", Effect: "writeTarget"},
	{Name: "writeError", Effect: "writeTarget", Error: true},
}

// clientCall is one controller-runtime client call of the reconciler.
type clientCall struct {
	Effect      string
	Operation   string
	Consistency string
	// Object is the abstraction object the call addresses.
	Object string
	Key    ir.ObjectRef
}

// reconcileFacts is what the generators need from the reconciler.
type reconcileFacts struct {
	Process ir.Process
	Calls   map[string]clientCall // by effect
}

var extracted struct {
	once  sync.Once
	facts reconcileFacts
	err   error
}

// extractReconcile extracts Reconciler.Reconcile once per test run.
func extractReconcile() (reconcileFacts, error) {
	extracted.once.Do(func() { extracted.facts, extracted.err = extractFacts() })
	return extracted.facts, extracted.err
}

// extractFacts lowers Reconciler.Reconcile with the typed goflow frontend.
// Client calls are summarized by symbol: a Get through the injected
// client.Client reads the informer cache, a Get through a client.Reader and
// every write go to the API (the controller-runtime manager's split client).
// Each call is bound to the effect the effect bindings declare for its
// resource, operation and consistency, and to the abstraction object its key
// expression addresses in the adapter fixture.
func extractFacts() (reconcileFacts, error) {
	var facts reconcileFacts
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return facts, err
	}
	l, err := goflow.Load(root, goflow.Selector{Package: "./examples/configmirror", Receiver: "Reconciler", Method: "Reconcile"})
	if err != nil {
		return facts, err
	}
	m, err := Load("system.yaml")
	if err != nil {
		return facts, err
	}
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		return facts, err
	}
	info := l.Package.TypesInfo
	type pending struct {
		call     clientCall
		object   string // Go variable holding the object
		key      string // Go expression of the key, for reads
		resource effect.Resource
	}
	var calls []pending
	summarize := func(c goflow.Callee, call *ast.CallExpr) (goflow.Node, bool, error) {
		if c.Package != clientPackage {
			return goflow.Node{}, false, nil
		}
		p := pending{}
		var obj ast.Expr
		switch {
		case c.Method == "Get" && len(call.Args) >= 3:
			p.call.Operation, p.key, obj = "get", goflow.Code(call.Args[1]), call.Args[2]
			p.call.Consistency = "direct"
			if c.Receiver == "Client" {
				p.call.Consistency = "cached"
			}
		case c.Method == "Update" && len(call.Args) >= 2:
			p.call.Operation, p.call.Consistency, obj = "update", "direct", call.Args[1]
		default:
			return goflow.Node{}, false, fmt.Errorf("%s: unsupported client call %s", l.Package.Fset.Position(call.Pos()), goflow.Code(call))
		}
		p.object = goflow.Code(obj)
		var err error
		if p.resource, err = resourceOf(info.TypeOf(obj)); err != nil {
			return goflow.Node{}, false, err
		}
		d, err := m.Effects().Lookup(p.resource, p.call.Operation, p.call.Consistency)
		if err != nil {
			return goflow.Node{}, false, err
		}
		p.call.Effect = d.ID
		calls = append(calls, p)
		return goflow.Node{Kind: p.call.Operation, Consistency: p.call.Consistency, Effect: d.ID}, true, nil
	}
	// The scope is the abstraction's state, plus the Go variables that hold
	// the objects the reconciler reads into: goflow binds each declaration
	// to its variable, which carries no model state of its own.
	scope := map[string]expr.Symbol{}
	for name, v := range a.StateVariables() {
		scope[name] = expr.Symbol{Type: v.Type, Quint: name}
	}
	ast.Inspect(l.Func.Body, func(n ast.Node) bool {
		if x, ok := n.(*ast.AssignStmt); ok && emptyObject(x) {
			name := x.Lhs[0].(*ast.Ident).Name
			scope[name] = expr.Symbol{Type: "bool", Quint: name}
		}
		return true
	})
	opts := goflow.Options{
		Scope:    scope,
		Returns:  []goflow.Return{{Type: "bool"}, {Error: true}},
		Targets:  reconcilerWrites,
		Bindings: map[string]expr.Expression{"reconcile.Result{}": "false"},
		Call: func(c goflow.Callee, call *ast.CallExpr, _ goflow.Convert) (goflow.Node, bool, error) {
			return summarize(c, call)
		},
	}
	for code, v := range reconcilerValues {
		opts.Bindings[code] = v
	}
	opts.Statement = func(s ast.Stmt, _ goflow.Convert) (goflow.Node, bool, error) {
		switch x := s.(type) {
		case *ast.AssignStmt:
			// Declaring an empty object to read into changes no model state.
			if emptyObject(x) {
				return goflow.Node{Kind: ir.NodeAssign}, true, nil
			}
		case *ast.ReturnStmt:
			// return reconcile.Result{}, r.Client.Update(ctx, target): the
			// call is the last effect; the node returns its error.
			if len(x.Results) != 2 {
				return goflow.Node{}, false, nil
			}
			call, ok := x.Results[1].(*ast.CallExpr)
			if !ok {
				return goflow.Node{}, false, nil
			}
			c, ok := calleeOf(info, call)
			if !ok {
				return goflow.Node{}, false, nil
			}
			n, ok, err := summarize(c, call)
			if !ok || err != nil {
				return goflow.Node{}, false, err
			}
			return goflow.Node{Kind: ir.NodeReturn, Effect: n.Effect, Consistency: n.Consistency}, true, nil
		}
		return goflow.Node{}, false, nil
	}
	program, err := goflow.ExtractTyped(l, opts)
	if err != nil {
		return facts, err
	}
	rel, err := filepath.Rel(root, l.Path)
	if err != nil {
		return facts, err
	}
	if facts.Process, err = toProcess(program, filepath.ToSlash(rel)); err != nil {
		return facts, err
	}

	// Resolve each call's object: reads by their key expression, writes by
	// the key the written variable was read with.
	readKeys := map[string]string{}
	for _, p := range calls {
		if p.key != "" {
			readKeys[p.object] = p.key
		}
	}
	facts.Calls = map[string]clientCall{}
	for _, p := range calls {
		key := p.key
		if key == "" {
			key = readKeys[p.object]
		}
		k, ok := reconcilerKeys[key]
		if !ok {
			return facts, fmt.Errorf("effect %s addresses %q, which the fixture does not bind", p.call.Effect, key)
		}
		p.call.Object, p.call.Key, err = objectAt(a, p.resource, k.Namespace, k.Name)
		if err != nil {
			return facts, fmt.Errorf("effect %s: %w", p.call.Effect, err)
		}
		if _, dup := facts.Calls[p.call.Effect]; dup {
			return facts, fmt.Errorf("effect %s is performed twice", p.call.Effect)
		}
		facts.Calls[p.call.Effect] = p.call
	}
	return facts, nil
}

// emptyObject reports whether an assignment declares a variable holding a
// new, empty object, such as source := &corev1.ConfigMap{}.
func emptyObject(x *ast.AssignStmt) bool {
	if len(x.Lhs) != 1 || len(x.Rhs) != 1 || x.Tok != token.DEFINE {
		return false
	}
	if _, ok := x.Lhs[0].(*ast.Ident); !ok {
		return false
	}
	u, ok := x.Rhs[0].(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return false
	}
	lit, ok := u.X.(*ast.CompositeLit)
	return ok && len(lit.Elts) == 0
}

// calleeOf resolves the method a call invokes through type information.
func calleeOf(info *types.Info, call *ast.CallExpr) (goflow.Callee, bool) {
	f, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return goflow.Callee{}, false
	}
	sel, ok := info.Selections[f]
	if !ok || sel.Kind() != types.MethodVal {
		return goflow.Callee{}, false
	}
	c := goflow.Callee{Method: sel.Obj().Name()}
	if sel.Obj().Pkg() != nil {
		c.Package = sel.Obj().Pkg().Path()
	}
	recv := sel.Recv()
	if p, ok := recv.(*types.Pointer); ok {
		recv = p.Elem()
	}
	if n, ok := types.Unalias(recv).(*types.Named); ok {
		c.Receiver = n.Obj().Name()
	}
	return c, true
}

// resourceOf maps a typed core API object to its resource.
func resourceOf(t types.Type) (effect.Resource, error) {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != "k8s.io/api/core/v1" {
		return effect.Resource{}, fmt.Errorf("object of type %s is not a core/v1 API type", t)
	}
	return effect.Resource{Version: "v1", Kind: n.Obj().Name()}, nil
}

// objectAt finds the abstraction object of a resource with a namespace and
// name.
func objectAt(a abstraction.Abstraction, res effect.Resource, namespace, name string) (string, ir.ObjectRef, error) {
	gvk := ir.GVK{Group: res.Group, Version: res.Version, Kind: res.Kind}
	for _, r := range a.Spec.Resources {
		if r.GVK != gvk {
			continue
		}
		for _, o := range r.Objects {
			if o.Namespace == namespace && o.Name == name {
				return o.ID, ir.ObjectRef{GVK: gvk, Namespace: namespace, Name: name}, nil
			}
		}
	}
	return "", ir.ObjectRef{}, fmt.Errorf("%s %s/%s is not an object of the abstraction", gvk, namespace, name)
}

// toProcess converts the extracted program into an IR process. Client
// operations become effect nodes; a return that returns a call's error
// becomes that effect followed by the return.
func toProcess(p *goflow.Program, file string) (ir.Process, error) {
	id := func(n int) string { return "n" + strconv.Itoa(n) }
	out := ir.Process{ID: "configmirror-reconcile", Controller: "Reconciler", Method: "Reconcile", Entry: id(p.Entry)}
	for _, n := range p.Nodes {
		src := ir.Source{File: file, Line: n.Line}
		switch {
		case n.Kind == ir.NodeReturn && n.Effect != "":
			ret := id(n.ID) + "r"
			out.Nodes = append(out.Nodes, ir.Node{ID: id(n.ID), Kind: ir.NodeEffect, Effect: n.Effect, Next: ret, Source: src}, ir.Node{ID: ret, Kind: ir.NodeReturn, Source: src})
		case n.Kind == ir.NodeReturn:
			out.Nodes = append(out.Nodes, ir.Node{ID: id(n.ID), Kind: ir.NodeReturn, Source: src})
		case n.Kind == ir.NodeBranch:
			out.Nodes = append(out.Nodes, ir.Node{ID: id(n.ID), Kind: ir.NodeBranch, Expression: string(n.Expression), Next: id(n.Next), Else: id(n.Else), Source: src})
		case n.Kind == ir.NodeAssign:
			var updates []string
			for k, v := range n.Updates {
				updates = append(updates, k+" = "+string(v))
			}
			sort.Strings(updates)
			out.Nodes = append(out.Nodes, ir.Node{ID: id(n.ID), Kind: ir.NodeAssign, Expression: strings.Join(updates, "; "), Next: id(n.Next), Source: src})
		case n.Effect != "":
			out.Nodes = append(out.Nodes, ir.Node{ID: id(n.ID), Kind: ir.NodeEffect, Effect: n.Effect, Next: id(n.Next), Source: src})
		default:
			return out, fmt.Errorf("node %d of kind %s has no IR form", n.ID, n.Kind)
		}
	}
	return out, nil
}

// readObjects lists, in order, the objects the reconciler reads with the
// given consistency ("" for any) and, when readOnly is set, never writes.
func (f reconcileFacts) readObjects(consistency string, readOnly bool) []string {
	written := map[string]bool{}
	for _, c := range f.Calls {
		if c.Operation != "get" {
			written[c.Object] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range f.Calls {
		if c.Operation != "get" || (consistency != "" && c.Consistency != consistency) || (readOnly && written[c.Object]) || seen[c.Object] {
			continue
		}
		seen[c.Object] = true
		out = append(out, c.Object)
	}
	sort.Strings(out)
	return out
}

// adapterProjection generates the projection from the abstraction document:
// every object's fields through the API, and the fields of the objects the
// reconciler reads through its cache also through the cache reader.
func adapterProjection() ([]byte, error) {
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		return nil, err
	}
	facts, err := extractReconcile()
	if err != nil {
		return nil, err
	}
	ps, err := adaptergen.FromSpec(a.Spec, facts.readObjects("cached", false)...)
	if err != nil {
		return nil, err
	}
	return adaptergen.GenerateReaderProjection(generatedPackage, ps)
}

// adapterEnvironment generates the environment: the environment changes
// the objects the reconciler only reads, and delivers the objects it reads
// through its cache.
func adapterEnvironment() ([]byte, error) {
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		return nil, err
	}
	facts, err := extractReconcile()
	if err != nil {
		return nil, err
	}
	return adaptergen.GenerateReaderEnvironment(generatedPackage, a.Spec, facts.readObjects("", true), facts.readObjects("cached", false))
}

// adapterGating generates boundary gating from the extracted process, each
// effect pinned to the object it addresses.
func adapterGating() ([]byte, error) {
	facts, err := extractReconcile()
	if err != nil {
		return nil, err
	}
	targets := map[string]ir.ObjectRef{}
	for id, c := range facts.Calls {
		targets[id] = c.Key
	}
	return adaptergen.GenerateBoundaryGating(generatedPackage, facts.Process, boundaryEvents, targets)
}

func TestAdapterGeneratedFilesUpToDate(t *testing.T) {
	for file, generate := range map[string]func() ([]byte, error){
		"projection_generated.go":  adapterProjection,
		"environment_generated.go": adapterEnvironment,
		"gating_generated.go":      adapterGating,
	} {
		t.Run(file, func(t *testing.T) {
			src, err := generate()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("generated", file)
			if *regenerateAdapter {
				if err := os.WriteFile(path, src, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			have, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(have) != string(src) {
				t.Fatalf("%s is stale; run go test ./examples/configmirror -run TestAdapterGeneratedFilesUpToDate -regenerate-adapter", path)
			}
		})
	}
}

// The extracted reconciler reads the source through the cache, reads the
// copy from the API and writes it; the generators derive the cached and
// environment-changed objects from exactly that.
func TestAdapterExtractedProgram(t *testing.T) {
	facts, err := extractReconcile()
	if err != nil {
		t.Fatal(err)
	}
	configMap := ir.GVK{Version: "v1", Kind: "ConfigMap"}
	want := map[string]clientCall{
		"readSource":  {Effect: "readSource", Operation: "get", Consistency: "cached", Object: "source", Key: ir.ObjectRef{GVK: configMap, Namespace: "demo", Name: "source"}},
		"readTarget":  {Effect: "readTarget", Operation: "get", Consistency: "direct", Object: "copy", Key: ir.ObjectRef{GVK: configMap, Namespace: "demo", Name: "copy"}},
		"writeTarget": {Effect: "writeTarget", Operation: "update", Consistency: "direct", Object: "copy", Key: ir.ObjectRef{GVK: configMap, Namespace: "demo", Name: "copy"}},
	}
	if !reflect.DeepEqual(facts.Calls, want) {
		t.Fatalf("client calls %+v, want %+v", facts.Calls, want)
	}
	if got := facts.readObjects("cached", false); !reflect.DeepEqual(got, []string{"source"}) {
		t.Fatalf("cached objects %v", got)
	}
	if got := facts.readObjects("", true); !reflect.DeepEqual(got, []string{"source"}) {
		t.Fatalf("read-only objects %v", got)
	}
	// The effects follow one another along the process, each guarded by
	// a branch on its error.
	var effects []string
	byID := map[string]ir.Node{}
	for _, n := range facts.Process.Nodes {
		byID[n.ID] = n
	}
	for id, steps := facts.Process.Entry, 0; id != "" && steps < len(facts.Process.Nodes); steps++ {
		n := byID[id]
		if n.Kind == ir.NodeEffect {
			effects = append(effects, n.Effect)
		}
		id = n.Next
		if n.Kind == ir.NodeBranch {
			id = n.Else
		}
	}
	if !reflect.DeepEqual(effects, []string{"readSource", "readTarget", "writeTarget"}) {
		t.Fatalf("effects along the success path %v", effects)
	}
}

// Every boundary event is an action of the handwritten model labelled with
// the same effect, and every effect-labelled action is a boundary event.
func TestAdapterBoundaryEventsMatchModel(t *testing.T) {
	var spec model.Spec
	if _, err := krm.Read("system.yaml", "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	labelled := map[string]string{}
	for _, a := range spec.Actions {
		if a.Effect != "" {
			labelled[a.Name] = a.Effect
		}
	}
	events := map[string]string{}
	for _, e := range boundaryEvents {
		events[e.Name] = e.Effect
	}
	if !reflect.DeepEqual(events, labelled) {
		t.Fatalf("boundary events %v, model actions with effects %v", events, labelled)
	}
}

package configmirror

import (
	"fmt"
	"go/ast"
	"go/types"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/process"
	"github.com/phoban01/generative-architecture/pkg/project"
	"github.com/phoban01/generative-architecture/pkg/summaries"
)

// This file derives the ConfigMirror model from its authored inputs. The
// facts about the controller that are not read from its source, the
// Abstraction document or the summary library come from project.yaml (see
// Settings); each is stated by symbol, never by source text.

// clientPackage declares the controller-runtime client interfaces.
const clientPackage = "sigs.k8s.io/controller-runtime/pkg/client"

// clientEffect is what a call of one client method means: the effect it
// performs, its operation key in the summary library and its consistency.
// The consistency follows the supported injection pattern: Client is the
// manager's client, whose reads are served by the informer cache, and
// APIReader reads the API server directly.
type clientEffect struct {
	Effect, Operation, Consistency string
}

// requestKey is the symbol of the reconcile request's key field.
const requestKey = "sigs.k8s.io/controller-runtime/pkg/reconcile.Request.NamespacedName"

// Settings are the facts about the controller that neither its source, the
// Abstraction document nor the summary library carries. The project
// document declares them under spec.controllers, spec.initial and
// spec.runtime, as it does for arch generate.
type Settings struct {
	// Effects is what a call of each client method means.
	Effects map[goflow.Callee]clientEffect
	// KeyObjects names, by field symbol, the abstraction object each key
	// expression addresses. The reconcile request's key maps to the
	// controller's key object.
	KeyObjects map[string]string
	// Locals keeps a selected field of a Go object local in a declared
	// abstraction local, by "<local>.<path>".
	Locals map[string]string
	// Initial is the initial value of each object's selected field.
	Initial map[string]string
	// Runtime is the controller's runtime profile.
	Runtime struct {
		Name                          string
		Workers, RetryTicks, MaxTicks int
	}
}

// clientOperations maps a client method to its summary operation.
var clientOperations = map[string]string{"Get": ir.OpGet, "Update": ir.OpUpdate}

// NewSettings reads the settings of the sample's one controller from its
// project document. The consistency of an effect follows the injection
// pattern unless declared: a read through the manager's client (receiver
// Client) is served by the informer cache, any other call goes to the API
// server.
func NewSettings(p project.Project) (Settings, error) {
	if len(p.Spec.Controllers) != 1 {
		return Settings{}, fmt.Errorf("project %s declares %d controllers, want one", p.Name, len(p.Spec.Controllers))
	}
	decl := p.Spec.Controllers[0]
	s := Settings{Effects: map[goflow.Callee]clientEffect{}, KeyObjects: map[string]string{requestKey: decl.Key}, Locals: decl.Locals, Initial: p.Spec.Initial}
	for _, e := range decl.Effects {
		pkg, receiver, method, err := e.Symbol()
		if err != nil {
			return Settings{}, err
		}
		op, ok := clientOperations[method]
		if !ok {
			return Settings{}, fmt.Errorf("%s: method %s is not supported; supported methods are Get and Update", e.Call, method)
		}
		consistency := e.Consistency
		if consistency == "" {
			consistency = ir.ConsistencyDirect
			if op == ir.OpGet && receiver == "Client" {
				consistency = ir.ConsistencyCached
			}
		}
		s.Effects[goflow.Callee{Package: pkg, Receiver: receiver, Method: method}] = clientEffect{e.Effect, op, consistency}
	}
	for symbol, object := range decl.ObjectKeys {
		s.KeyObjects[symbol] = object
	}
	s.Runtime.Name, s.Runtime.Workers, s.Runtime.RetryTicks, s.Runtime.MaxTicks = decl.Name, decl.Workers, decl.RetryTicks, p.Spec.Runtime.MaxTicks
	return s, nil
}

// versionPath is the JSON path of the object version the summaries compare.
var versionPath = []string{"metadata", "resourceVersion"}

// extraction holds the model names extraction binds Go objects to.
type extraction struct {
	abs      abstraction.Abstraction
	info     *types.Info
	body     *ast.BlockStmt
	locals   map[string]abstraction.Resource // Go object local -> its resource
	resource map[ir.GVK]abstraction.Resource
	settings Settings
}

// localVariable is the model variable holding the selected field f of a Go
// object local: the local the project declares for it, else
// <local>_<name> for a named field and <local>_<path elements> otherwise.
func (x *extraction) localVariable(local string, f abstraction.Field) string {
	if v, ok := x.settings.Locals[local+"."+abstraction.PathKey(f.Path)]; ok {
		return v
	}
	if f.Name != "" {
		return local + "_" + f.Name
	}
	return local + "_" + strings.Join(abstraction.JSONPath(f.Path), "_")
}

// cacheVariable is the informer cache's copy of an object variable, named
// as the generated replay projection names the cache reader's view.
func cacheVariable(v string) string { return adaptergen.CachedVariable(v) }

// ExtractOptions builds the typed-frontend options for Reconcile: client
// calls resolve by callee symbol to their effects and summary bindings, and
// fields of object locals resolve by JSON path to model variables. It uses
// no statement callback and no source-text map.
func ExtractOptions(l goflow.Loaded, abs abstraction.Abstraction, s Settings) (goflow.Options, error) {
	x := &extraction{abs: abs, info: l.Package.TypesInfo, body: l.Func.Body, locals: map[string]abstraction.Resource{}, resource: map[ir.GVK]abstraction.Resource{}, settings: s}
	for _, r := range abs.Spec.Resources {
		x.resource[r.GVK] = r
	}
	scope := map[string]expr.Symbol{}
	var err error
	ast.Inspect(l.Func.Body, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignStmt)
		if !ok || err != nil || len(a.Lhs) != 1 {
			return true
		}
		id, ok := a.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		obj, ok := x.info.Defs[id].(*types.Var)
		if !ok {
			return true
		}
		gvk, ok := resourceOf(obj.Type())
		if !ok {
			return true
		}
		r, ok := x.resource[gvk]
		if !ok {
			err = fmt.Errorf("local %s has type %s, which the abstraction does not select", id.Name, gvk)
			return false
		}
		x.locals[id.Name] = r
		for _, f := range r.Fields {
			v := x.localVariable(id.Name, f)
			scope[v] = expr.Symbol{Type: f.Type, Quint: v}
		}
		return true
	})
	if err != nil {
		return goflow.Options{}, err
	}
	opts := goflow.Options{File: l.Path, Scope: scope, Returns: []goflow.Return{{Zero: true}, {Error: true}}}
	opts.Call = x.call
	opts.Field = x.field
	opts.MapKeys = x.mapKeys
	return opts, nil
}

// resourceOf maps a Go API type (or pointer to one) declared in
// k8s.io/api/<group>/<version> to its GVK; the core group is "".
func resourceOf(t types.Type) (ir.GVK, bool) {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return ir.GVK{}, false
	}
	pkg := n.Obj().Pkg().Path()
	if !strings.HasPrefix(pkg, "k8s.io/api/") {
		return ir.GVK{}, false
	}
	group, version := path.Split(strings.TrimPrefix(pkg, "k8s.io/api/"))
	group = strings.TrimSuffix(group, "/")
	if group == "core" {
		group = ""
	}
	return ir.GVK{Group: group, Version: version, Kind: n.Obj().Name()}, true
}

// field maps a selected field of a Go object local to its model variable.
func (x *extraction) field(root string, fieldPath []string) (expr.Expression, bool) {
	r, ok := x.locals[root]
	if !ok {
		return "", false
	}
	for _, f := range r.Fields {
		if abstraction.PathKey(fieldPath) == abstraction.PathKey(abstraction.JSONPath(f.Path)) {
			return expr.Expression(x.localVariable(root, f)), true
		}
	}
	return "", false
}

// call summarizes a client call: its effect, operation, consistency and
// the bindings of the summary placeholders to the addressed object's
// variables and the Go local the object is read into or written from.
func (x *extraction) call(c goflow.Callee, call *ast.CallExpr, _ goflow.Convert) (goflow.Node, bool, error) {
	e, ok := x.settings.Effects[c]
	if !ok {
		return goflow.Node{}, false, nil
	}
	if len(call.Args) < 2 {
		return goflow.Node{}, false, fmt.Errorf("%s: unexpected arity", c)
	}
	objArg := call.Args[len(call.Args)-1]
	local, r, err := x.local(objArg)
	if err != nil {
		return goflow.Node{}, false, err
	}
	var object string
	switch e.Operation {
	case ir.OpGet:
		if object, err = x.key(call.Args[1]); err != nil {
			return goflow.Node{}, false, err
		}
	default:
		// A write addresses the object the local was read from.
		if object, err = x.readInto(local); err != nil {
			return goflow.Node{}, false, err
		}
	}
	// The summary placeholders that stand for the object's content bind one
	// variable per selected field, in field order (see goflow.Node.Vectors).
	values := make([]string, 0, len(r.Fields))
	snapshots := make([]string, 0, len(r.Fields))
	cachedValues := make([]string, 0, len(r.Fields))
	for _, f := range r.Fields {
		value := abstraction.FieldVariable(r.GVK.Kind, object, f)
		values = append(values, value)
		snapshots = append(snapshots, x.localVariable(local, f))
		cachedValues = append(cachedValues, cacheVariable(value))
	}
	version := abstraction.VariableName(r.GVK.Kind, object, versionPath)
	b := map[string]string{
		"$exists":          abstraction.ExistsVariable(r.GVK.Kind, object),
		"$snapshotVersion": x.localVariable(local, abstraction.Field{Path: versionPath}),
	}
	vectors := map[string][]string{}
	switch {
	case e.Operation == ir.OpGet && e.Consistency == ir.ConsistencyCached:
		// Objects are neither created nor deleted while the model runs, so
		// the cache's view of existence is the API server's.
		b["$cacheVersion"] = cacheVariable(version)
		vectors["$cache"], vectors["$snapshot"] = cachedValues, snapshots
	case e.Operation == ir.OpGet:
		b["$resourceVersion"] = version
		vectors["$object"], vectors["$snapshot"] = values, snapshots
	case e.Operation == ir.OpUpdate:
		b["$resourceVersion"] = version
		vectors["$object"], vectors["$value"] = values, snapshots
	default:
		return goflow.Node{}, false, fmt.Errorf("%s: no bindings for operation %s", c, e.Operation)
	}
	return goflow.Node{Kind: e.Operation, Effect: e.Effect, Consistency: e.Consistency, Bindings: b, Vectors: vectors}, true, nil
}

// local resolves the object argument of a client call to a Go object local.
func (x *extraction) local(arg ast.Expr) (string, abstraction.Resource, error) {
	if u, ok := ast.Unparen(arg).(*ast.UnaryExpr); ok {
		arg = u.X
	}
	id, ok := ast.Unparen(arg).(*ast.Ident)
	if !ok {
		return "", abstraction.Resource{}, fmt.Errorf("client call object must be a local, got %s", goflow.Code(arg))
	}
	r, ok := x.locals[id.Name]
	if !ok {
		return "", abstraction.Resource{}, fmt.Errorf("client call object %s is not an abstracted object local", id.Name)
	}
	return id.Name, r, nil
}

// key resolves a key argument, a field selection, to an abstraction object
// through the symbol of the selected field.
func (x *extraction) key(arg ast.Expr) (string, error) {
	sel, ok := ast.Unparen(arg).(*ast.SelectorExpr)
	if !ok {
		return "", fmt.Errorf("object key must be a field selection, got %s", goflow.Code(arg))
	}
	s, ok := x.info.Selections[sel]
	if !ok || s.Kind() != types.FieldVal {
		return "", fmt.Errorf("object key %s is not a field", goflow.Code(arg))
	}
	recv := s.Recv()
	if p, ok := types.Unalias(recv).(*types.Pointer); ok {
		recv = p.Elem()
	}
	n, ok := types.Unalias(recv).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return "", fmt.Errorf("object key %s is not a field of a named type", goflow.Code(arg))
	}
	symbol := n.Obj().Pkg().Path() + "." + n.Obj().Name() + "." + s.Obj().Name()
	object, ok := x.settings.KeyObjects[symbol]
	if !ok {
		return "", fmt.Errorf("object key %s addresses no declared object", symbol)
	}
	return object, nil
}

// readInto finds the object a Go local was read into: the key of the
// unique read whose object argument is that local.
func (x *extraction) readInto(local string) (string, error) {
	var found []string
	ast.Inspect(x.body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if !ok {
			return true
		}
		s, ok := x.info.Selections[sel]
		if !ok || s.Kind() != types.MethodVal || s.Obj().Pkg() == nil {
			return true
		}
		recv := types.Unalias(s.Recv())
		if p, ok := recv.(*types.Pointer); ok {
			recv = types.Unalias(p.Elem())
		}
		named, ok := recv.(*types.Named)
		if !ok {
			return true
		}
		e, ok := x.settings.Effects[goflow.Callee{Package: s.Obj().Pkg().Path(), Receiver: named.Obj().Name(), Method: s.Obj().Name()}]
		if !ok || e.Operation != ir.OpGet {
			return true
		}
		if l, _, err := x.local(call.Args[2]); err != nil || l != local {
			return true
		}
		if object, err := x.key(call.Args[1]); err == nil {
			found = append(found, object)
		}
		return true
	})
	sort.Strings(found)
	if len(found) != 1 {
		return "", fmt.Errorf("local %s is written but read from %d objects %v; expected exactly one", local, len(found), found)
	}
	return found[0], nil
}

// Runtime events: the actions of the controller-runtime profile the model
// composes with the process, which replay drives through the worker.
const (
	EventEnqueue     = "enqueue"
	EventBegin       = "begin"
	EventFinish      = "finish"
	EventFinishError = "finishError"
	// The requeue outcomes a return node reports through the outcome
	// variable; the runtime model schedules the key again after them.
	EventFinishRequeue    = "finishRequeue"
	EventFinishAfter      = "finishAfter"
	EventFinishErrorAfter = "finishErrorAfter"
	EventAdvance          = "advance"
	EventFire             = "fire"
)

// Model variables the composition adds to the abstraction's state.
const (
	pcVariable       = "pc"
	finishedVariable = "finished"
	failedVariable   = "failed"
	// outcomeVariable holds the runtime outcome of the last return: success,
	// error, requeue, after or errorAfter (see process.Outcome*).
	outcomeVariable = "outcome"
)

// Generate derives the ConfigMirror TransitionModel from the authored
// inputs in the example directory root: state from the
// Abstraction document, the reconcile process from the typed extraction of
// Reconcile expanded with the built-in Get and Update summaries, the queue,
// timer and clock from controllerruntime.BuildGroupModel for one controller
// and one key, environment actions from the abstraction's value domains and
// the cached reads, and properties from the runtime model and
// constraints.yaml, which is written against the generated names.
//
// The package is loaded from root itself; inputs_test.go checks that
// project.yaml selects exactly this package and constraints document.
func Generate(root string) (model.Spec, error) {
	return GenerateOverlay(root, nil)
}

// GenerateOverlay is Generate with Go source files replaced by overlay
// (see goflow.LoadOptions), so a mutated controller rebuilds the model
// without being written to disk.
func GenerateOverlay(root string, overlay map[string][]byte) (model.Spec, error) {
	p, err := project.Load(filepath.Join(root, "project.yaml"))
	if err != nil {
		return model.Spec{}, err
	}
	s, err := NewSettings(p)
	if err != nil {
		return model.Spec{}, err
	}
	abs, err := abstraction.Load(filepath.Join(root, "abstraction.yaml"))
	if err != nil {
		return model.Spec{}, err
	}
	l, err := goflow.LoadWith(root, goflow.Selector{Package: ".", Receiver: "Reconciler", Method: "Reconcile"}, goflow.LoadOptions{Overlay: overlay})
	if err != nil {
		return model.Spec{}, err
	}
	opts, err := ExtractOptions(l, abs, s)
	if err != nil {
		return model.Spec{}, err
	}
	program, err := goflow.ExtractTyped(l, opts)
	if err != nil {
		return model.Spec{}, err
	}
	var constraints struct {
		Properties []model.Property `yaml:"properties"`
	}
	if _, err = krm.Read(filepath.Join(root, "constraints.yaml"), "ModelConstraints", &constraints); err != nil {
		return model.Spec{}, err
	}
	return compose(s, abs, *program, constraints.Properties)
}

// objectInfo is an abstracted object and its model variables.
type objectInfo struct {
	resource        abstraction.Resource
	object          abstraction.Object
	exists, version string
	// values are the variables of the selected fields, in field order.
	values []string
}

// objectsOf keys every abstracted object by its exists variable.
func objectsOf(abs abstraction.Abstraction) map[string]objectInfo {
	out := map[string]objectInfo{}
	for _, r := range abs.Spec.Resources {
		for _, o := range r.Objects {
			out[abstraction.ExistsVariable(r.GVK.Kind, o.ID)] = objectInfo{resource: r, object: o,
				values:  fieldVariables(r, o),
				exists:  abstraction.ExistsVariable(r.GVK.Kind, o.ID),
				version: abstraction.VariableName(r.GVK.Kind, o.ID, versionPath)}
		}
	}
	return out
}

func compose(s Settings, abs abstraction.Abstraction, program goflow.Program, constraints []model.Property) (model.Spec, error) {
	objects := objectsOf(abs)
	state := abs.StateVariables()
	for _, o := range objects {
		for i, f := range o.resource.Fields {
			initial, ok := initialValue(s.Initial, o.resource, o.object.ID, f)
			if !ok {
				return model.Spec{}, fmt.Errorf("object %s has no initial value for field %s; declare it under spec.initial as %s", o.object.ID, f.Key(), o.object.ID+"."+f.Key())
			}
			if !slices.Contains(o.resource.ValueDomains[f.Key()], initial) {
				return model.Spec{}, fmt.Errorf("object %s: initial value %q of field %s is not in its value domain", o.object.ID, initial, f.Key())
			}
			state[o.values[i]] = model.Variable{Type: f.Type, Initial: expr.Expression(initial)}
		}
		state[o.exists] = model.Variable{Type: abstraction.TypeBool, Initial: "true"}
		state[o.version] = model.Variable{Type: abstraction.TypeInt, Initial: "0"}
	}
	// Process locals are the variables summaries and assignments bind Go
	// object locals and errors to; they are reset when an invocation
	// begins.
	locals := map[string]model.Variable{
		failedVariable:  {Type: abstraction.TypeBool, Initial: "false"},
		outcomeVariable: {Type: abstraction.TypeString, Initial: expr.Expression(strconv.Quote(process.OutcomeSuccess))},
	}
	cached := map[string]objectInfo{}
	reads, writes := map[string]objectInfo{}, map[string]objectInfo{}
	var effects []effect.Descriptor
	seenEffects := map[string]bool{}
	for _, n := range program.Nodes {
		if n.Error != "" {
			locals[string(n.Error)] = model.Variable{Type: abstraction.TypeBool, Initial: "false"}
		}
		// A constant error result (nil or a literal) binds no local.
		if n.ReturnError != "" && n.ReturnError != "true" && n.ReturnError != "false" {
			locals[string(n.ReturnError)] = model.Variable{Type: abstraction.TypeBool, Initial: "false"}
		}
		if n.Effect == "" {
			continue
		}
		o, ok := objects[n.Bindings["$exists"]]
		if !ok {
			return model.Spec{}, fmt.Errorf("effect %s addresses no abstracted object", n.Effect)
		}
		if !seenEffects[n.Effect] {
			seenEffects[n.Effect] = true
			effects = append(effects, effect.Descriptor{ID: n.Effect, Resource: effect.Resource{Group: o.resource.GVK.Group, Version: o.resource.GVK.Version, Kind: o.resource.GVK.Kind}, Operation: n.Kind, Consistency: n.Consistency})
		}
		for placeholder, name := range n.Bindings {
			switch placeholder {
			case "$snapshotVersion":
				locals[name] = model.Variable{Type: abstraction.TypeInt, Initial: "0"}
			case "$cacheVersion":
				state[name] = model.Variable{Type: abstraction.TypeInt, Initial: "0"}
			}
		}
		for placeholder, names := range n.Vectors {
			if len(names) != len(o.resource.Fields) {
				return model.Spec{}, fmt.Errorf("effect %s binds %s to %d variables for %d fields", n.Effect, placeholder, len(names), len(o.resource.Fields))
			}
			for i, name := range names {
				f := o.resource.Fields[i]
				switch placeholder {
				case "$snapshot", "$value":
					if _, declared := state[name]; !declared {
						locals[name] = model.Variable{Type: f.Type, Initial: expr.Expression(abstraction.Initial(f.Type))}
					}
				case "$cache":
					state[name] = state[o.values[i]]
				}
			}
		}
		switch {
		case n.Kind == ir.OpGet && n.Consistency == ir.ConsistencyCached:
			reads[o.exists], cached[o.exists] = o, o
		case n.Kind == ir.OpGet:
			reads[o.exists] = o
		default:
			writes[o.exists] = o
		}
	}
	sort.Slice(effects, func(i, j int) bool { return effects[i].ID < effects[j].ID })
	reset := map[string]expr.Expression{}
	for name, v := range locals {
		state[name] = v
		reset[name] = v.Initial
	}
	for _, l := range abs.Spec.Locals {
		reset[l.Name] = state[l.Name].Initial
	}

	spec, err := process.Expand(model.Spec{Name: "ConfigMirror", State: state, Effects: effects, Domains: abs.Spec.Populations()}, program, process.Config{
		PC: pcVariable, Finished: finishedVariable, Outcome: outcomeVariable, Results: []string{"", failedVariable}, Summaries: summaries.Builtin(),
	})
	if err != nil {
		return model.Spec{}, err
	}
	// The process is idle until an invocation begins.
	spec.State[finishedVariable] = model.Variable{Type: abstraction.TypeBool, Initial: "true"}
	renameEffectActions(&spec, program)

	runtime, err := runtimeActions(s, abs, program, reset)
	if err != nil {
		return model.Spec{}, err
	}
	for name, v := range runtime.State {
		spec.State[name] = v
	}
	actions := environmentActions(reads, writes, cached)
	actions = append(actions, runtime.Actions...)
	spec.Actions = append(actions, spec.Actions...)
	spec.Properties = runtime.Properties
	spec.Properties = append(spec.Properties, constraints...)
	if _, err := model.Compile(spec); err != nil {
		return model.Spec{}, err
	}
	return spec, nil
}

// renameEffectActions names the outcome actions of an effect node
// <effect>_<outcome> instead of node<id>_<outcome>.
func renameEffectActions(spec *model.Spec, program goflow.Program) {
	count := map[string]int{}
	for _, n := range program.Nodes {
		if n.Effect != "" {
			count[n.Effect]++
		}
	}
	for _, n := range program.Nodes {
		if n.Effect == "" || count[n.Effect] != 1 {
			continue
		}
		prefix := fmt.Sprintf("node%d_", n.ID)
		for i, a := range spec.Actions {
			if strings.HasPrefix(a.Name, prefix) {
				spec.Actions[i].Name = n.Effect + "_" + strings.TrimPrefix(a.Name, prefix)
			}
		}
	}
}

// runtimeActions instantiates controllerruntime.BuildGroupModel for one
// controller and one key and composes it with the process: begin is the
// queue's start plus the process entry, and the finish outcomes follow the
// returned error. Reconcile always returns the zero reconcile.Result
// (extraction checks it), so the requeue, after and errorAfter outcomes
// cannot occur, and the client errors it returns are never terminal. With
// a single key the queue order variables carry no information and are
// dropped; the abstraction's invocation bound limits consecutive retries.
func runtimeActions(s Settings, abs abstraction.Abstraction, program goflow.Program, reset map[string]expr.Expression) (model.Spec, error) {
	g, binding, err := RuntimeModel(abs, s)
	if err != nil {
		return model.Spec{}, err
	}
	prefix := s.Runtime.Name + "_" + g.Profile.Controllers[0].Keys[0].ID + "_"
	names := map[string]expr.Expression{}
	out := model.Spec{State: map[string]model.Variable{}}
	for name, short := range binding.Variables {
		names[name] = expr.Expression(short)
		out.State[short] = g.Spec.State[name]
	}
	rename := func(e expr.Expression) (expr.Expression, error) { return expr.Rewrite(e, names) }
	for _, a := range g.Spec.Actions {
		name, ok := binding.Events[a.Name]
		if !ok {
			continue
		}
		guard, err := rename(a.Guard)
		if err != nil {
			return model.Spec{}, err
		}
		updates := map[string]expr.Expression{}
		for target, value := range a.Updates {
			if _, kept := binding.Variables[target]; !kept {
				continue
			}
			if updates[string(names[target])], err = rename(value); err != nil {
				return model.Spec{}, err
			}
		}
		switch name {
		case EventBegin:
			if abs.Spec.Bounds.MaxInvocations > 0 {
				guard = expr.Expression(fmt.Sprintf("%s && retries < %d", guard, abs.Spec.Bounds.MaxInvocations))
			}
			for k, v := range reset {
				updates[k] = v
			}
			updates[pcVariable] = expr.Expression(strconv.Itoa(program.Entry))
			updates[finishedVariable] = "false"
		case EventFinish, EventFinishError, EventFinishRequeue, EventFinishAfter, EventFinishErrorAfter:
			// A finish event fires when the process has returned with its
			// outcome, so no requeue request is dropped.
			guard = expr.Expression(fmt.Sprintf("%s && %s && %s == %q", guard, finishedVariable, outcomeVariable, finishOutcomes[name]))
		}
		out.Actions = append(out.Actions, model.Action{Name: name, Guard: guard, Updates: updates})
	}
	for _, p := range g.Spec.Properties {
		e, err := rename(p.Expression)
		if err != nil {
			return model.Spec{}, err
		}
		out.Properties = append(out.Properties, model.Property{Name: strings.TrimPrefix(strings.TrimPrefix(p.Name, prefix), s.Runtime.Name+"_"), Expression: e})
	}
	return out, nil
}

// RuntimeModel instantiates controllerruntime.BuildGroupModel for the
// runtime profile and the one key the reconcile request addresses, and
// binds it to the composed model: the runtime events keep the names above,
// and every state variable but the queue order loses the controller and key
// prefix. The adapter's worker routing and projection are generated from
// both (see gen_test.go).
func RuntimeModel(abs abstraction.Abstraction, s Settings) (cr.GroupModel, adaptergen.RuntimeBinding, error) {
	var key abstraction.Object
	for _, r := range abs.Spec.Resources {
		for _, o := range r.Objects {
			if o.ID == s.KeyObjects[requestKey] {
				key = o
			}
		}
	}
	if key.ID == "" {
		return cr.GroupModel{}, adaptergen.RuntimeBinding{}, fmt.Errorf("the reconcile key addresses no abstracted object")
	}
	g, err := cr.BuildGroupModel(cr.GroupProfile{Name: "ConfigMirror", MaxTicks: s.Runtime.MaxTicks, Controllers: []cr.ModelController{{
		Name: s.Runtime.Name, Workers: s.Runtime.Workers, RetryTicks: s.Runtime.RetryTicks, AfterTicks: s.Runtime.RetryTicks,
		Keys: []cr.ModelKey{{ID: key.ID, Namespace: key.Namespace, Name: key.Name}},
	}}})
	if err != nil {
		return cr.GroupModel{}, adaptergen.RuntimeBinding{}, err
	}
	prefix := s.Runtime.Name + "_" + key.ID + "_"
	dropped := map[string]bool{s.Runtime.Name + "_next": true, prefix + "order": true}
	b := adaptergen.RuntimeBinding{
		Events:    map[string]string{"advance": EventAdvance, prefix + "enqueue": EventEnqueue, prefix + "start": EventBegin, prefix + "fire": EventFire, prefix + "success": EventFinish, prefix + "error": EventFinishError, prefix + "requeue": EventFinishRequeue, prefix + "after": EventFinishAfter, prefix + "errorAfter": EventFinishErrorAfter},
		Variables: map[string]string{},
	}
	for name := range g.Spec.State {
		if !dropped[name] {
			b.Variables[name] = strings.TrimPrefix(name, prefix)
		}
	}
	return g, b, nil
}

// environmentActions lets the environment set an object the controller
// reads but never writes to each value of its domain (one action per
// value, named as adaptergen's per-value environment names its events),
// and deliver the API server's state of each cached object to the
// informer cache.
func environmentActions(reads, writes, cached map[string]objectInfo) []model.Action {
	var out []model.Action
	for _, exists := range sortedObjects(reads) {
		o := reads[exists]
		if _, written := writes[exists]; written {
			continue
		}
		for i, f := range o.resource.Fields {
			for j, v := range o.resource.ValueDomains[f.Key()] {
				out = append(out, model.Action{
					Name:    changeValueEvent(o.resource, o.object.ID, f, v, j),
					Guard:   expr.Expression(fmt.Sprintf("%s != %s", o.values[i], v)),
					Updates: map[string]expr.Expression{o.values[i]: expr.Expression(v), o.version: expr.Expression(o.version + " + 1")},
				})
			}
		}
	}
	for _, exists := range sortedObjects(cached) {
		o := cached[exists]
		name := adaptergen.DeliverCacheEvent
		if len(cached) > 1 {
			name += "_" + o.object.ID
		}
		updates := map[string]expr.Expression{cacheVariable(o.version): expr.Expression(o.version)}
		for _, v := range o.values {
			updates[cacheVariable(v)] = expr.Expression(v)
		}
		out = append(out, model.Action{Name: name, Guard: "true", Updates: updates})
	}
	return out
}

func sortedObjects(m map[string]objectInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// finishOutcomes maps each finish event to the outcome the process must
// report for it.
var finishOutcomes = map[string]string{
	EventFinish:           process.OutcomeSuccess,
	EventFinishError:      process.OutcomeError,
	EventFinishRequeue:    process.OutcomeRequeue,
	EventFinishAfter:      process.OutcomeAfter,
	EventFinishErrorAfter: process.OutcomeErrorAfter,
}

// mapKeys lists the selected keys of the map at jsonPath on the object
// local root: the last element of every selected field path whose parent
// is jsonPath. goflow clears the keys a map literal omits, so a replaced
// map does not keep stale values.
func (x *extraction) mapKeys(root string, jsonPath []string) []string {
	r, ok := x.locals[root]
	if !ok {
		return nil
	}
	var keys []string
	for _, f := range r.Fields {
		p := abstraction.JSONPath(f.Path)
		if len(p) == len(jsonPath)+1 && abstraction.PathKey(p[:len(p)-1]) == abstraction.PathKey(jsonPath) {
			keys = append(keys, p[len(p)-1])
		}
	}
	return keys
}

// fieldVariables names the model variables of the selected fields of o, in
// field order.
func fieldVariables(r abstraction.Resource, o abstraction.Object) []string {
	out := make([]string, 0, len(r.Fields))
	for _, f := range r.Fields {
		out = append(out, abstraction.FieldVariable(r.GVK.Kind, o.ID, f))
	}
	return out
}

// initialValue finds the declared initial value of field f of object id:
// spec.initial names it by the object ID alone when the resource selects
// one field, or by <object>.<field key>.
func initialValue(initial map[string]string, r abstraction.Resource, id string, f abstraction.Field) (string, bool) {
	if v, ok := initial[id+"."+f.Key()]; ok {
		return v, true
	}
	if len(r.Fields) == 1 {
		v, ok := initial[id]
		return v, ok
	}
	return "", false
}

// changeValueEvent names the environment event that sets field f of object
// id to the index-th value of its domain: the per-object name when the
// resource selects one field, else the object and the field key.
func changeValueEvent(r abstraction.Resource, id string, f abstraction.Field, literal string, index int) string {
	if len(r.Fields) == 1 {
		return adaptergen.ChangeValueEvent(id, literal, index)
	}
	return adaptergen.ChangeValueEvent(id+"_"+strings.ReplaceAll(f.Key(), ".", "_"), literal, index)
}

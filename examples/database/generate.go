package database

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/dynamicgen"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/process"
	"github.com/phoban01/generative-architecture/pkg/watchgen"
	"github.com/phoban01/generative-architecture/pkg/watchmodel"
)

// This file derives the Database controller's TransitionModel from its
// authored inputs:
//   - controller.go: the reconcile process, extracted from Reconcile;
//   - wiring.go: the watches, extracted from SetupWithManager;
//   - dynamic_store.go with resources.yaml: the resource and operation each
//     Store method reaches through the dynamic client;
//   - abstraction.yaml: the state, its initial values and value domains;
//   - process.yaml: the Store's business summaries, written against the
//     abstraction's variable names.
//
// The declarations below are the only facts about the controller that are
// not read from those inputs. Each is stated by symbol, never by source text.

// storeEffects names the effect each method of the Store interface performs.
// Extraction matches a call by the method it invokes on Reconcile's Store
// parameter; dynamic_store.go's accesses confirm the resource and operation
// of every method that reaches the API.
var storeEffects = map[string]string{
	"DatabaseExists":                "readDatabase",
	"SatisfiesProvisioningContract": "checkContract",
	"DatabaseInstance":              "readInstance",
	"CreateDatabaseInstance":        "createInstance",
	"UpdateDatabaseStatus":          "publish",
}

// derivedEffects are effects of Store methods that reach no API object: each
// reads a derived field, named here, of an object an earlier effect read.
// The effect is labelled with that field's resource.
var derivedEffects = map[string]string{"checkContract": "contractSatisfied"}

// Operation and consistency of a derived read. It evaluates content the
// controller already holds, so it has no API consistency.
const (
	derivedOperation   = "derive"
	derivedConsistency = "schema"
)

// invocationCounter is the abstraction local that counts reconcile
// invocations. The environment creates the objects the controller only reads
// before the first invocation: the abstraction excludes later revocation.
const invocationCounter = "invocations"

// EnvironmentAction is an environment action of the generated model: it
// creates the object Object of resource Kind with Variable set to Value, or
// updates Variable of the existing object to Value. Value is a CEL literal.
type EnvironmentAction struct {
	Name, Operation, Kind, Object, Variable, Value string
}

// Environment operations.
const (
	EnvCreate = "create"
	EnvUpdate = "update"
)

// spec is the generated model before the watch summary is composed.
type generation struct {
	Spec        model.Spec
	Program     goflow.Program
	Environment []EnvironmentAction
	// Results names, per effect, the Go locals Reconcile assigns the
	// results of its Store call to, in result order.
	Results map[string][]string
}

// generate derives the TransitionModel of Reconcile in source from the
// abstraction, the dynamic accesses and the Store's business summaries.
// The watch summary is composed by the caller.
func generate(abs abstraction.Abstraction, source []byte, accesses dynamicgen.Result, config process.Config) (generation, error) {
	effects, err := generateEffects(abs, accesses)
	if err != nil {
		return generation{}, err
	}
	state := abs.StateVariables()
	written, err := controllerWrites(abs, config)
	if err != nil {
		return generation{}, err
	}
	env, err := environmentActions(abs, written)
	if err != nil {
		return generation{}, err
	}
	program, goLocals, results, err := extract(source, state)
	if err != nil {
		return generation{}, err
	}
	actions := make([]model.Action, 0, len(env))
	for _, a := range env {
		actions = append(actions, a.action(abs))
	}
	spec, err := process.Expand(model.Spec{Name: "DatabaseWalkthrough", State: state, Effects: effects, Actions: actions}, program, config)
	if err != nil {
		return generation{}, err
	}
	// Invocation mechanics: the process is idle until a reconcile begins it,
	// and returns to the scheduler on every Go return. A reconcile resets
	// the Go locals of Reconcile and its results; the other abstraction
	// locals are counters that persist across invocations.
	spec.State[config.PC] = model.Variable{Type: abstraction.TypeInt, Initial: "-1"}
	spec.State[config.Finished] = model.Variable{Type: abstraction.TypeBool, Initial: "true"}
	reset := map[string]expr.Expression{
		config.PC: expr.Expression(strconv.Itoa(program.Entry)), config.Finished: "false",
		invocationCounter: invocationCounter + " + 1",
	}
	for _, name := range append(goLocals, config.Results...) {
		v, ok := state[name]
		if !ok {
			return generation{}, fmt.Errorf("Reconcile local %s is not an abstraction local", name)
		}
		reset[name] = v.Initial
	}
	if _, ok := state[invocationCounter]; !ok {
		return generation{}, fmt.Errorf("abstraction declares no %s local", invocationCounter)
	}
	guard := expr.Expression(config.Finished)
	if max := abs.Spec.Bounds.MaxInvocations; max > 0 {
		guard = expr.Expression(fmt.Sprintf("%s && %s < %d", config.Finished, invocationCounter, max))
	}
	spec.Actions = append(spec.Actions, model.Action{Name: "reconcile", Guard: guard, Updates: reset})
	// Between invocations the system may stay idle, so bounded traces need
	// not end in a reconcile.
	spec.Actions = append(spec.Actions, model.Action{Name: "idle", Guard: expr.Expression(config.Finished), Updates: map[string]expr.Expression{}})
	return generation{Spec: spec, Program: program, Environment: env, Results: results}, nil
}

// generateEffects lists one effect per Store method: the extracted dynamic
// access of a method that reaches the API, or a derived read of the
// resource of the field derivedEffects names. Effects are sorted by ID.
func generateEffects(abs abstraction.Abstraction, accesses dynamicgen.Result) ([]effect.Descriptor, error) {
	byEffect := map[string]effect.Descriptor{}
	for _, a := range accesses.Accesses {
		if storeEffects[a.Method] != a.Effect {
			return nil, fmt.Errorf("dynamic method %s performs %s, but Store declares %s", a.Method, a.Effect, storeEffects[a.Method])
		}
		d := effect.Descriptor{ID: a.Effect, Resource: a.Binding.Resource, Operation: a.Operation, Consistency: "direct"}
		if prior, dup := byEffect[a.Effect]; dup && prior != d {
			return nil, fmt.Errorf("effect %s has two different accesses", a.Effect)
		}
		byEffect[a.Effect] = d
	}
	for id, field := range derivedEffects {
		r, _, ok := fieldNamed(abs, field)
		if !ok {
			return nil, fmt.Errorf("derived effect %s reads %s, which the abstraction does not declare", id, field)
		}
		byEffect[id] = effect.Descriptor{ID: id, Resource: effect.Resource{Group: r.GVK.Group, Version: r.GVK.Version, Kind: r.GVK.Kind}, Operation: derivedOperation, Consistency: derivedConsistency}
	}
	var out []effect.Descriptor
	for method, id := range storeEffects {
		d, ok := byEffect[id]
		if !ok {
			return nil, fmt.Errorf("Store method %s: effect %s has neither a dynamic access nor a derived field", method, id)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// fieldNamed finds the abstraction field whose key (its name, else its
// dot-joined path) is key.
func fieldNamed(abs abstraction.Abstraction, key string) (abstraction.Resource, abstraction.Field, bool) {
	for _, r := range abs.Spec.Resources {
		for _, f := range r.Fields {
			if f.Key() == key {
				return r, f, true
			}
		}
	}
	return abstraction.Resource{}, abstraction.Field{}, false
}

// controllerWrites collects the abstraction variables the Store's summaries
// assign: an object's exists flag when the controller creates it, and the
// fields it writes.
func controllerWrites(abs abstraction.Abstraction, config process.Config) (map[string]bool, error) {
	state := abs.StateVariables()
	out := map[string]bool{}
	for op, variants := range config.Operations {
		for _, v := range variants {
			for target := range v.Updates {
				if _, ok := state[target]; !ok {
					return nil, fmt.Errorf("summary %s assigns %s, which the abstraction does not declare", op, target)
				}
				out[target] = true
			}
		}
	}
	return out, nil
}

// environmentActions derives what the environment does from the abstraction
// and the controller's writes. An object the controller never creates is
// created by the environment before the first invocation, once for each
// value of the one field the controller does not write; the controller's
// fields start at their zero value. In an object the controller creates,
// the one field it does not write is set by the environment once, from its
// zero value to each other value of its domain. Values never return to
// zero: the abstraction excludes revocation. More than one field the
// environment owns per object is not supported.
func environmentActions(abs abstraction.Abstraction, written map[string]bool) ([]EnvironmentAction, error) {
	var out []EnvironmentAction
	for _, r := range abs.Spec.Resources {
		for _, o := range r.Objects {
			var owned []abstraction.Field
			for _, f := range r.Fields {
				if !written[abstraction.FieldVariable(r.GVK.Kind, o.ID, f)] {
					owned = append(owned, f)
				}
			}
			if len(owned) != 1 {
				return nil, fmt.Errorf("object %s: the environment owns %d fields; generation supports exactly one", o.ID, len(owned))
			}
			f := owned[0]
			domain, ok := r.ValueDomains[f.Key()]
			if !ok {
				return nil, fmt.Errorf("object %s: field %s has no value domain", o.ID, f.Key())
			}
			variable := abstraction.FieldVariable(r.GVK.Kind, o.ID, f)
			created := written[abstraction.ExistsVariable(r.GVK.Kind, o.ID)]
			for i, v := range domain {
				a := EnvironmentAction{Kind: r.GVK.Kind, Object: o.ID, Variable: variable, Value: v}
				switch {
				case !created:
					a.Name, a.Operation = CreateEvent(o.ID, v, i), EnvCreate
				case v != abstraction.Initial(f.Type):
					a.Name, a.Operation = adaptergen.ChangeValueEvent(o.ID, v, i), EnvUpdate
				default:
					continue
				}
				out = append(out, a)
			}
		}
	}
	return out, nil
}

// CreateEvent is the environment event that creates object objectID with
// its environment-owned field set to literal, the index-th value of its
// domain. It is named as adaptergen names a per-value change, with create
// in place of change.
func CreateEvent(objectID, literal string, index int) string {
	return "create" + strings.TrimPrefix(adaptergen.ChangeValueEvent(objectID, literal, index), "change")
}

// action renders an environment action as a model action. A created object
// starts with every other field at its zero value.
func (a EnvironmentAction) action(abs abstraction.Abstraction) model.Action {
	exists := abstraction.ExistsVariable(a.Kind, a.Object)
	if a.Operation == EnvUpdate {
		return model.Action{
			Name:    a.Name,
			Guard:   expr.Expression(fmt.Sprintf("%s && %s == %s", exists, a.Variable, abs.StateVariables()[a.Variable].Initial)),
			Updates: map[string]expr.Expression{a.Variable: expr.Expression(a.Value)},
		}
	}
	return model.Action{
		Name:    a.Name,
		Guard:   expr.Expression(fmt.Sprintf("!%s && %s == 0", exists, invocationCounter)),
		Updates: map[string]expr.Expression{exists: "true", a.Variable: expr.Expression(a.Value)},
	}
}

// EnvironmentEvent returns the environment action that sets variable to
// the CEL literal value.
func (e Example) EnvironmentEvent(variable, value string) (EnvironmentAction, error) {
	for _, a := range e.Environment {
		if a.Variable == variable && a.Value == value {
			return a, nil
		}
	}
	return EnvironmentAction{}, fmt.Errorf("no environment action sets %s to %s", variable, value)
}

// extract lowers Reconcile in source. A statement that consists of a call
// of a Store method on Reconcile's Store parameter becomes a node of the
// method's effect; its arguments lower to the node's expression with the
// model type of the parameter the Store interface declares. It returns the
// program, the names of the Go locals Reconcile assigns and, per effect,
// the locals its call's results are assigned to, in result order ("" for a
// result left unassigned).
func extract(source []byte, state map[string]model.Variable) (goflow.Program, []string, map[string][]string, error) {
	params, store, err := storeSignature(source)
	if err != nil {
		return goflow.Program{}, nil, nil, err
	}
	scope := map[string]expr.Symbol{}
	for name, v := range state {
		scope[name] = expr.Symbol{Type: v.Type, Quint: name}
	}
	locals := map[string]bool{}
	results := map[string][]string{}
	opts := goflow.Options{File: "controller.go", Method: "Reconcile", Scope: scope, Returns: []goflow.Return{{Type: abstraction.TypeString}}}
	opts.Statement = func(s ast.Stmt, convert goflow.Convert) (goflow.Node, bool, error) {
		var call *ast.CallExpr
		var assigned []string
		switch x := s.(type) {
		case *ast.ExprStmt:
			call, _ = x.X.(*ast.CallExpr)
		case *ast.AssignStmt:
			if len(x.Rhs) == 1 {
				call, _ = x.Rhs[0].(*ast.CallExpr)
			}
			for _, lhs := range x.Lhs {
				name := ""
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
					locals[id.Name] = true
					name = id.Name
				}
				assigned = append(assigned, name)
			}
		}
		if call == nil {
			return goflow.Node{}, false, nil
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return goflow.Node{}, false, nil
		}
		if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != store {
			return goflow.Node{}, false, nil
		}
		kind, ok := storeEffects[sel.Sel.Name]
		if !ok {
			return goflow.Node{}, false, fmt.Errorf("Store method %s performs no declared effect", sel.Sel.Name)
		}
		if prev, seen := results[kind]; seen && !slices.Equal(prev, assigned) {
			return goflow.Node{}, false, fmt.Errorf("Store method %s: its results are assigned to %v and to %v", sel.Sel.Name, prev, assigned)
		}
		results[kind] = assigned
		types := params[sel.Sel.Name]
		if len(call.Args) != len(types) || len(types) > 1 {
			return goflow.Node{}, false, fmt.Errorf("Store method %s: %d arguments; at most one is supported", sel.Sel.Name, len(call.Args))
		}
		n := goflow.Node{Kind: kind}
		if len(types) == 1 {
			n.Expression, err = convert(call.Args[0], types[0])
		}
		return n, true, err
	}
	p, err := goflow.Extract(source, opts)
	if err != nil {
		return goflow.Program{}, nil, nil, err
	}
	names := make([]string, 0, len(locals))
	for name := range locals {
		names = append(names, name)
	}
	sort.Strings(names)
	return *p, names, results, nil
}

// storeSignature parses controller.go for the Store interface's method
// parameter types, as model types, and the name of Reconcile's Store
// parameter.
func storeSignature(source []byte) (map[string][]string, string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "controller.go", source, 0)
	if err != nil {
		return nil, "", err
	}
	params := map[string][]string{}
	store := ""
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			for _, s := range d.Specs {
				t, ok := s.(*ast.TypeSpec)
				if !ok || t.Name.Name != "Store" {
					continue
				}
				iface, ok := t.Type.(*ast.InterfaceType)
				if !ok {
					return nil, "", fmt.Errorf("Store is not an interface")
				}
				for _, m := range iface.Methods.List {
					fn, ok := m.Type.(*ast.FuncType)
					if !ok || len(m.Names) != 1 {
						return nil, "", fmt.Errorf("Store embeds an interface")
					}
					var types []string
					for _, p := range fn.Params.List {
						id, ok := p.Type.(*ast.Ident)
						if !ok || !slices.Contains([]string{abstraction.TypeString, abstraction.TypeBool, abstraction.TypeInt}, id.Name) {
							return nil, "", fmt.Errorf("Store.%s: parameter type %s has no model type", m.Names[0].Name, goflow.Code(p.Type))
						}
						for range max(1, len(p.Names)) {
							types = append(types, id.Name)
						}
					}
					params[m.Names[0].Name] = types
				}
			}
		case *ast.FuncDecl:
			if d.Recv != nil || d.Name.Name != "Reconcile" {
				continue
			}
			for _, p := range d.Type.Params.List {
				if id, ok := p.Type.(*ast.Ident); ok && id.Name == "Store" && len(p.Names) == 1 {
					store = p.Names[0].Name
				}
			}
		}
	}
	if store == "" || len(params) == 0 {
		return nil, "", fmt.Errorf("controller.go declares no Store interface or Reconcile(store Store)")
	}
	for method := range storeEffects {
		if _, ok := params[method]; !ok {
			return nil, "", fmt.Errorf("Store declares no method %s", method)
		}
	}
	return params, store, nil
}

// LoadSources generates the model from the example directory root, with
// controller.go and wiring.go replaced by source and wiring.
func LoadSources(root string, source, wiring []byte) (Example, error) {
	watches, err := extractWatches(wiring)
	if err != nil {
		return Example{}, err
	}
	abs, err := abstraction.Load(filepath.Join(root, "abstraction.yaml"))
	if err != nil {
		return Example{}, err
	}
	var config process.Config
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
	api := map[string]string{}
	for method, id := range storeEffects {
		if _, derived := derivedEffects[id]; !derived {
			api[method] = id
		}
	}
	accesses, err := dynamicgen.Extract(dynamicSource, "DynamicStore", "s.Client", api, catalog)
	if err != nil {
		return Example{}, err
	}
	g, err := generate(abs, source, accesses, config)
	if err != nil {
		return Example{}, err
	}
	if err = accesses.CheckEffects(g.Spec.Effects); err != nil {
		return Example{}, err
	}
	registry, err := effect.NewRegistry(g.Spec.Effects)
	if err != nil {
		return Example{}, err
	}
	for _, rule := range watches.Rules {
		if _, err = registry.Lookup(rule.Resource, "get", "direct"); err != nil {
			return Example{}, fmt.Errorf("watch identity does not match this slice's API resources: %w", err)
		}
	}
	spec, watchState, emits, err := composeWatches(g.Spec, watches, g.Environment, abs)
	if err != nil {
		return Example{}, err
	}
	compiled, err := model.Compile(spec)
	if err != nil {
		return Example{}, err
	}
	var requirements struct {
		Properties []model.Property `yaml:"properties"`
	}
	if _, err := krm.Read(filepath.Join(root, "constraints.yaml"), "ModelConstraints", &requirements); err != nil {
		return Example{}, err
	}
	constrained := spec
	constrained.Properties = requirements.Properties
	e := Example{Dynamic: accesses, Resources: catalog, Watches: watches, Program: g.Program, Spec: spec, Environment: g.Environment, Model: compiled, Results: g.Results, emits: emits, Locals: map[string]string{}}
	for _, l := range abs.Spec.Locals {
		e.Locals[l.Name] = l.Type
	}
	if e.ReconcileProcess, err = e.eventProcess(); err != nil {
		return Example{}, err
	}
	e.Observed = append(watchState, abs.SortedVariables()...)
	e.Constrained, err = model.Compile(constrained)
	return e, err
}

// watchController and watchFile locate the extracted watches in IR
// provenance.
const (
	watchController = "DatabaseReconciler"
	watchFile       = "wiring.go"
)

// extractWatches reads the watch registrations of SetupWithManager in
// wiring. The native delivery in the adapter supports the primary Database
// and DatabaseInstance through InstanceToDatabase.
func extractWatches(wiring []byte) (watchgen.Result, error) {
	resources := map[string]effect.Resource{}
	for _, kind := range []string{"Database", "DatabaseInstance"} {
		resources["github.com/phoban01/generative-architecture/examples/database/api/v1alpha1."+kind] = effect.Resource{Group: "demo.architecture.local", Version: "v1alpha1", Kind: kind}
	}
	w, err := watchgen.Extract(wiring, watchController, "SetupWithManager", resources)
	if err != nil {
		return w, err
	}
	for _, r := range w.Rules {
		self := r.Mapping == "self" && r.Resource.Kind == "Database"
		mapped := r.Mapping == "InstanceToDatabase" && r.Resource.Kind == "DatabaseInstance"
		if !self && !mapped {
			return w, fmt.Errorf("native delivery has no handler for %s with mapping %s", r.Resource.Kind, r.Mapping)
		}
	}
	return w, nil
}

// composeWatches adds the watch model that pkg/watchmodel generates from
// the extracted watches. The objects and the reconcile keys come from the
// abstraction: the keys are the objects of the primary resource. Each
// action that changes a watched object records its event with Emit. It
// returns the spec, the state the watch model added, sorted, and the event
// each action raises.
func composeWatches(spec model.Spec, w watchgen.Result, env []EnvironmentAction, abs abstraction.Abstraction) (model.Spec, []string, map[string]watchEvent, error) {
	watches := w.IR(watchController, watchFile)
	watched := map[string]ir.GVK{}
	var primary ir.GVK
	for _, x := range watches {
		watched[x.Resource.Kind] = x.Resource
		if x.Primary {
			primary = x.Resource
		}
	}
	var objects, keys []ir.ObjectRef
	refs := map[string]ir.ObjectRef{}
	for _, r := range abs.Spec.Resources {
		for _, o := range r.Objects {
			ref := ir.ObjectRef{GVK: r.GVK, Namespace: o.Namespace, Name: o.Name}
			objects = append(objects, ref)
			refs[r.GVK.Kind] = ref
			if r.GVK == primary {
				keys = append(keys, ref)
			}
		}
	}
	if len(keys) != 1 {
		return spec, nil, nil, fmt.Errorf("the reconcile action pops one key; the abstraction declares %d", len(keys))
	}
	fields, err := watchFields(abs, watches, refs)
	if err != nil {
		return spec, nil, nil, err
	}
	out, err := watchmodel.Add(spec, watches, objects, keys, watchmodel.WithFields(fields))
	if err != nil {
		return spec, nil, nil, err
	}
	queued := watchmodel.QueuedVar(keys[0])
	// A delayed event can refer to an object already gone by reconciliation
	// time: the environment removes the Database before the first read.
	out.Actions = append(out.Actions, model.Action{Name: removeBeforeRead, Guard: expr.Expression(databaseExistsVariable + " && !" + instanceExistsVariable + " && " + invocationCounter + " == 0"), Updates: map[string]expr.Expression{databaseExistsVariable: "false", contractVariable: "false", databaseEndpointVariable: `""`}})
	emits, err := watchEmits(out, env, abs)
	if err != nil {
		return spec, nil, nil, err
	}
	for i := range out.Actions {
		a := &out.Actions[i]
		updates := make(map[string]expr.Expression, len(a.Updates))
		for k, v := range a.Updates {
			updates[k] = v
		}
		// An object nobody watches raises no event.
		if e, ok := emits[a.Name]; ok && watched[e.kind] != (ir.GVK{}) {
			u, err := watchmodel.Emit(watched[e.kind], e.event, e.generation)
			if err != nil {
				return spec, nil, nil, err
			}
			for k, v := range u {
				if _, dup := updates[k]; dup {
					return spec, nil, nil, fmt.Errorf("action %s already updates %s", a.Name, k)
				}
				updates[k] = v
			}
		}
		// The worker pops the key: a reconcile needs a queued key and
		// clears it, so an event during processing queues it again.
		if a.Name == "reconcile" {
			a.Guard = expr.Expression("(" + string(a.Guard) + ") && " + queued)
			updates[queued] = "false"
		}
		a.Updates = updates
	}
	var added []string
	for name := range out.State {
		if _, ok := spec.State[name]; !ok {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	return out, added, emits, nil
}

// watchFields supplies the value of each field a field mapping reads, from
// the abstraction field at the same path. That field must have one value
// in its domain. A path the abstraction does not declare stays unsupplied,
// and watchmodel then treats the target as outside the key set.
func watchFields(abs abstraction.Abstraction, watches []ir.Watch, refs map[string]ir.ObjectRef) (map[ir.ObjectRef]map[string]string, error) {
	out := map[ir.ObjectRef]map[string]string{}
	for _, w := range watches {
		if w.Mapping.Kind != ir.MappingField {
			continue
		}
		for _, r := range abs.Spec.Resources {
			if r.GVK != w.Resource {
				continue
			}
			for _, f := range r.Fields {
				if !goPath(f.Path, w.Mapping.FieldPath) {
					continue
				}
				domain := r.ValueDomains[f.Key()]
				if len(domain) != 1 {
					return nil, fmt.Errorf("watch on %s maps through %s, which needs one value in its domain; found %d", r.GVK.Kind, f.Key(), len(domain))
				}
				value, err := strconv.Unquote(domain[0])
				if err != nil {
					return nil, fmt.Errorf("field %s: %w", f.Key(), err)
				}
				ref := refs[r.GVK.Kind]
				if out[ref] == nil {
					out[ref] = map[string]string{}
				}
				out[ref][strings.Join(w.Mapping.FieldPath, ".")] = value
			}
		}
	}
	return out, nil
}

// goPath reports whether the Go field path names the API path: each Go
// field is the API field with its first letter in upper case.
func goPath(api, fields []string) bool {
	if len(api) != len(fields) {
		return false
	}
	for i := range api {
		if api[i] == "" || strings.ToUpper(api[i][:1])+api[i][1:] != fields[i] {
			return false
		}
	}
	return true
}

// removeBeforeRead is the environment action that deletes the Database
// before the first reconcile reads it.
const removeBeforeRead = "removeDatabaseBeforeRead"

// watchEvent is the event an action raises on the object of kind.
type watchEvent struct {
	kind, event string
	generation  bool
}

// watchEmits finds the event each action raises. An environment action
// creates or updates its object; an update of a spec field moves the
// generation. An effect action raises the event of its API operation.
// removeDatabaseBeforeRead deletes the Database.
func watchEmits(spec model.Spec, env []EnvironmentAction, abs abstraction.Abstraction) (map[string]watchEvent, error) {
	out := map[string]watchEvent{removeBeforeRead: {kind: "Database", event: watchmodel.Delete}}
	for _, e := range env {
		switch e.Operation {
		case EnvCreate:
			out[e.Name] = watchEvent{kind: e.Kind, event: watchmodel.Create}
		case EnvUpdate:
			path, ok := variablePath(abs, e.Kind, e.Object, e.Variable)
			if !ok {
				return nil, fmt.Errorf("environment action %s sets %s, which the abstraction does not declare", e.Name, e.Variable)
			}
			out[e.Name] = watchEvent{kind: e.Kind, event: watchmodel.Update, generation: path[0] == "spec"}
		default:
			return nil, fmt.Errorf("environment action %s: unknown operation %s", e.Name, e.Operation)
		}
	}
	effects := map[string]effect.Descriptor{}
	for _, d := range spec.Effects {
		effects[d.ID] = d
	}
	for _, a := range spec.Actions {
		if a.Effect == "" {
			continue
		}
		d, ok := effects[a.Effect]
		if !ok {
			return nil, fmt.Errorf("action %s performs undeclared effect %s", a.Name, a.Effect)
		}
		e := watchEvent{kind: d.Resource.Kind}
		switch d.Operation {
		case "get", "list", derivedOperation:
			continue
		case "create":
			e.event = watchmodel.Create
		case "update":
			e.event, e.generation = watchmodel.Update, true
		case "status.update":
			e.event = watchmodel.Update
		case "delete":
			e.event = watchmodel.Delete
		default:
			return nil, fmt.Errorf("effect %s: operation %s has no watch event", d.ID, d.Operation)
		}
		out[a.Name] = e
	}
	return out, nil
}

// variablePath returns the API path of the field of object whose state
// variable is variable.
func variablePath(abs abstraction.Abstraction, kind, object, variable string) ([]string, bool) {
	for _, r := range abs.Spec.Resources {
		if r.GVK.Kind != kind {
			continue
		}
		for _, f := range r.Fields {
			if abstraction.FieldVariable(kind, object, f) == variable {
				return f.Path, true
			}
		}
	}
	return nil, false
}

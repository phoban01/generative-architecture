package main

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/adaptergen"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/process"
	"generative-architecture/pkg/project"
	"generative-architecture/pkg/summaries"
)

// Runtime events: the actions of the controller-runtime profile the model
// composes with the process.
const (
	eventEnqueue     = "enqueue"
	eventBegin       = "begin"
	eventFinish      = "finish"
	eventFinishError = "finishError"
	eventAdvance     = "advance"
	eventFire        = "fire"
)

// Model variables the composition adds to the abstraction's state.
const (
	pcVariable       = "pc"
	finishedVariable = "finished"
	failedVariable   = "failed"
)

// controllerModel is the input of one controller's model.
type controllerModel struct {
	name     string // model name
	abs      abstraction.Abstraction
	decl     project.Controller
	initial  map[string]string
	maxTicks int
	program  goflow.Program
	// properties are the authored constraints, written against the
	// generated names.
	properties []model.Property
}

// objectInfo is an abstracted object and its model variables.
type objectInfo struct {
	resource               abstraction.Resource
	object                 abstraction.Object
	value, exists, version string
}

// objectsOf keys every abstracted object by its exists variable.
func objectsOf(abs abstraction.Abstraction) map[string]objectInfo {
	out := map[string]objectInfo{}
	for _, r := range abs.Spec.Resources {
		for _, o := range r.Objects {
			out[abstraction.ExistsVariable(r.GVK.Kind, o.ID)] = objectInfo{resource: r, object: o,
				value:   abstraction.VariableName(r.GVK.Kind, o.ID, r.Fields[0].Path),
				exists:  abstraction.ExistsVariable(r.GVK.Kind, o.ID),
				version: abstraction.VariableName(r.GVK.Kind, o.ID, versionPath)}
		}
	}
	return out
}

// build derives the TransitionModel of one controller: state from the
// abstraction and the declared initial values, the reconcile process
// expanded with the built-in summaries, the queue, timer and clock of the
// controller-runtime group model for one key, environment actions from the
// value domains and the cached reads, and the runtime and authored
// properties.
func (c controllerModel) build() (model.Spec, []effect.Descriptor, error) {
	objects := objectsOf(c.abs)
	state := c.abs.StateVariables()
	for _, exists := range sortedObjects(objects) {
		o := objects[exists]
		initial, ok := c.initial[o.object.ID]
		if !ok {
			return model.Spec{}, nil, fmt.Errorf("object %s has no initial value; declare it under spec.initial", o.object.ID)
		}
		if !slices.Contains(o.resource.ValueDomains[abstraction.PathKey(o.resource.Fields[0].Path)], initial) {
			return model.Spec{}, nil, fmt.Errorf("object %s: initial value %s is not in its value domain", o.object.ID, initial)
		}
		state[o.exists] = model.Variable{Type: abstraction.TypeBool, Initial: "true"}
		state[o.value] = model.Variable{Type: o.resource.Fields[0].Type, Initial: expr.Expression(initial)}
		state[o.version] = model.Variable{Type: abstraction.TypeInt, Initial: "0"}
	}
	// Process locals are the variables summaries and assignments bind Go
	// object locals and errors to; they are reset when an invocation
	// begins.
	locals := map[string]model.Variable{failedVariable: {Type: abstraction.TypeBool, Initial: "false"}}
	cached := map[string]objectInfo{}
	reads, writes := map[string]objectInfo{}, map[string]objectInfo{}
	var effects []effect.Descriptor
	seenEffects := map[string]effect.Descriptor{}
	for _, n := range c.program.Nodes {
		if n.Error != "" {
			locals[string(n.Error)] = model.Variable{Type: abstraction.TypeBool, Initial: "false"}
		}
		if n.ReturnError != "" {
			locals[string(n.ReturnError)] = model.Variable{Type: abstraction.TypeBool, Initial: "false"}
		}
		if n.Effect == "" {
			continue
		}
		o, ok := objects[n.Bindings["$exists"]]
		if !ok {
			return model.Spec{}, nil, fmt.Errorf("effect %s addresses no abstracted object", n.Effect)
		}
		d := effect.Descriptor{ID: n.Effect, Resource: effect.Resource{Group: o.resource.GVK.Group, Version: o.resource.GVK.Version, Kind: o.resource.GVK.Kind}, Operation: n.Kind, Consistency: n.Consistency}
		if prior, seen := seenEffects[n.Effect]; !seen {
			seenEffects[n.Effect] = d
			effects = append(effects, d)
		} else if prior != d {
			return model.Spec{}, nil, fmt.Errorf("effect %s is used for %s and %s; declare one effect per resource and operation", n.Effect, prior.Resource.Kind, d.Resource.Kind)
		}
		field := model.Variable{Type: o.resource.Fields[0].Type, Initial: expr.Expression(abstraction.Initial(o.resource.Fields[0].Type))}
		for placeholder, name := range n.Bindings {
			switch placeholder {
			case "$snapshot", "$value":
				if _, declared := state[name]; !declared {
					locals[name] = field
				}
			case "$snapshotVersion":
				locals[name] = model.Variable{Type: abstraction.TypeInt, Initial: "0"}
			case "$cache":
				state[name] = state[o.value]
			case "$cacheVersion":
				state[name] = model.Variable{Type: abstraction.TypeInt, Initial: "0"}
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
	for _, l := range c.abs.Spec.Locals {
		reset[l.Name] = state[l.Name].Initial
	}

	spec, err := process.Expand(model.Spec{Name: c.name, State: state, Effects: effects}, c.program, process.Config{
		PC: pcVariable, Finished: finishedVariable, Results: []string{"", failedVariable}, Summaries: summaries.Builtin(),
	})
	if err != nil {
		return model.Spec{}, nil, err
	}
	// The process is idle until an invocation begins.
	spec.State[finishedVariable] = model.Variable{Type: abstraction.TypeBool, Initial: "true"}
	renameEffectActions(&spec, c.program)

	runtime, err := c.runtimeActions(reset)
	if err != nil {
		return model.Spec{}, nil, err
	}
	for name, v := range runtime.State {
		spec.State[name] = v
	}
	actions := environmentActions(reads, writes, cached)
	actions = append(actions, runtime.Actions...)
	spec.Actions = append(actions, spec.Actions...)
	spec.Properties = runtime.Properties
	spec.Properties = append(spec.Properties, c.properties...)
	if _, err := model.Compile(spec); err != nil {
		return model.Spec{}, nil, err
	}
	return spec, effects, nil
}

// renameEffectActions names the outcome actions of an effect node
// <effect>_<outcome> instead of node<id>_<outcome> when the effect occurs
// once.
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

// profile is the controller-runtime group profile of the controller: one
// controller with one key, the object the reconcile request addresses.
func (c controllerModel) profile() (cr.GroupProfile, error) {
	var key abstraction.Object
	for _, r := range c.abs.Spec.Resources {
		for _, o := range r.Objects {
			if o.ID == c.decl.Key {
				key = o
			}
		}
	}
	if key.ID == "" {
		return cr.GroupProfile{}, fmt.Errorf("controller %s: key %s is not an abstracted object", c.decl.Name, c.decl.Key)
	}
	return cr.GroupProfile{Name: c.name, MaxTicks: c.maxTicks, Controllers: []cr.ModelController{{
		Name: c.decl.Name, Workers: c.decl.Workers, RetryTicks: c.decl.RetryTicks, AfterTicks: c.decl.RetryTicks,
		Keys: []cr.ModelKey{{ID: key.ID, Namespace: key.Namespace, Name: key.Name}},
	}}}, nil
}

// runtimeActions instantiates the controller-runtime group model for one
// controller and one key and composes it with the process: begin is the
// queue's start plus the process entry, and the finish outcomes follow the
// returned error. Extraction checks that Reconcile returns the zero
// reconcile.Result, so the requeue outcomes cannot occur. With a single key
// the queue order variables carry no information and are dropped; the
// abstraction's invocation bound limits consecutive retries.
func (c controllerModel) runtimeActions(reset map[string]expr.Expression) (model.Spec, error) {
	p, err := c.profile()
	if err != nil {
		return model.Spec{}, err
	}
	g, err := cr.BuildGroupModel(p)
	if err != nil {
		return model.Spec{}, err
	}
	controller, key := p.Controllers[0].Name, p.Controllers[0].Keys[0].ID
	prefix := controller + "_" + key + "_"
	dropped := map[string]bool{controller + "_next": true, prefix + "order": true}
	names := map[string]expr.Expression{}
	out := model.Spec{State: map[string]model.Variable{}}
	for name, v := range g.Spec.State {
		if dropped[name] {
			continue
		}
		short := strings.TrimPrefix(name, prefix)
		names[name] = expr.Expression(short)
		out.State[short] = v
	}
	rename := func(e expr.Expression) (expr.Expression, error) { return expr.Rewrite(e, names) }
	actionNames := map[string]string{"advance": eventAdvance, prefix + "enqueue": eventEnqueue, prefix + "start": eventBegin, prefix + "fire": eventFire, prefix + "success": eventFinish, prefix + "error": eventFinishError}
	for _, a := range g.Spec.Actions {
		name, ok := actionNames[a.Name]
		if !ok {
			continue
		}
		guard, err := rename(a.Guard)
		if err != nil {
			return model.Spec{}, err
		}
		updates := map[string]expr.Expression{}
		for target, value := range a.Updates {
			if dropped[target] {
				continue
			}
			if updates[string(names[target])], err = rename(value); err != nil {
				return model.Spec{}, err
			}
		}
		switch name {
		case eventBegin:
			if c.abs.Spec.Bounds.MaxInvocations > 0 {
				guard = expr.Expression(fmt.Sprintf("%s && retries < %d", guard, c.abs.Spec.Bounds.MaxInvocations))
			}
			for k, v := range reset {
				updates[k] = v
			}
			updates[pcVariable] = expr.Expression(strconv.Itoa(c.program.Entry))
			updates[finishedVariable] = "false"
		case eventFinish:
			guard = expr.Expression(fmt.Sprintf("%s && %s && !%s", guard, finishedVariable, failedVariable))
		case eventFinishError:
			guard = expr.Expression(fmt.Sprintf("%s && %s && %s", guard, finishedVariable, failedVariable))
		}
		out.Actions = append(out.Actions, model.Action{Name: name, Guard: guard, Updates: updates})
	}
	for _, p := range g.Spec.Properties {
		e, err := rename(p.Expression)
		if err != nil {
			return model.Spec{}, err
		}
		out.Properties = append(out.Properties, model.Property{Name: strings.TrimPrefix(strings.TrimPrefix(p.Name, prefix), controller+"_"), Expression: e})
	}
	return out, nil
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
		for i, v := range o.resource.ValueDomains[abstraction.PathKey(o.resource.Fields[0].Path)] {
			out = append(out, model.Action{
				Name:    adaptergen.ChangeValueEvent(o.object.ID, v, i),
				Guard:   expr.Expression(fmt.Sprintf("%s != %s", o.value, v)),
				Updates: map[string]expr.Expression{o.value: expr.Expression(v), o.version: expr.Expression(o.version + " + 1")},
			})
		}
	}
	for _, exists := range sortedObjects(cached) {
		o := cached[exists]
		name := adaptergen.DeliverCacheEvent
		if len(cached) > 1 {
			name += "_" + o.object.ID
		}
		out = append(out, model.Action{Name: name, Guard: "true", Updates: map[string]expr.Expression{adaptergen.CachedVariable(o.value): expr.Expression(o.value), adaptergen.CachedVariable(o.version): expr.Expression(o.version)}})
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

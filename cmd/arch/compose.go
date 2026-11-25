package main

import (
	"fmt"
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
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/process"
	"github.com/phoban01/generative-architecture/pkg/project"
	"github.com/phoban01/generative-architecture/pkg/summaries"
)

// Runtime events: the actions of the controller-runtime profile the model
// composes with the process.
const (
	eventEnqueue     = "enqueue"
	eventBegin       = "begin"
	eventFinish      = "finish"
	eventFinishError = "finishError"
	// The requeue outcomes a return node reports through the outcome
	// variable; the runtime model schedules the key again after them.
	eventFinishRequeue    = "finishRequeue"
	eventFinishAfter      = "finishAfter"
	eventFinishErrorAfter = "finishErrorAfter"
	eventAdvance          = "advance"
	eventFire             = "fire"
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
		for i, f := range o.resource.Fields {
			initial, ok := initialValue(c.initial, o.resource, o.object.ID, f)
			if !ok {
				return model.Spec{}, nil, fmt.Errorf("object %s has no initial value for field %s; declare it under spec.initial as %s", o.object.ID, f.Key(), o.object.ID+"."+f.Key())
			}
			if !slices.Contains(o.resource.ValueDomains[f.Key()], initial) {
				return model.Spec{}, nil, fmt.Errorf("object %s: initial value %s of field %s is not in its value domain", o.object.ID, initial, f.Key())
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
	seenEffects := map[string]effect.Descriptor{}
	for _, n := range c.program.Nodes {
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
			return model.Spec{}, nil, fmt.Errorf("effect %s addresses no abstracted object", n.Effect)
		}
		d := effect.Descriptor{ID: n.Effect, Resource: effect.Resource{Group: o.resource.GVK.Group, Version: o.resource.GVK.Version, Kind: o.resource.GVK.Kind}, Operation: n.Kind, Consistency: n.Consistency}
		if prior, seen := seenEffects[n.Effect]; !seen {
			seenEffects[n.Effect] = d
			effects = append(effects, d)
		} else if prior != d {
			return model.Spec{}, nil, fmt.Errorf("effect %s is used for %s and %s; declare one effect per resource and operation", n.Effect, prior.Resource.Kind, d.Resource.Kind)
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
				return model.Spec{}, nil, fmt.Errorf("effect %s binds %s to %d variables for %d fields", n.Effect, placeholder, len(names), len(o.resource.Fields))
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
	for _, l := range c.abs.Spec.Locals {
		reset[l.Name] = state[l.Name].Initial
	}

	spec, err := process.Expand(model.Spec{Name: c.name, State: state, Effects: effects, Domains: c.abs.Spec.Populations()}, c.program, process.Config{
		PC: pcVariable, Finished: finishedVariable, Outcome: outcomeVariable, Results: []string{"", failedVariable}, Summaries: summaries.Builtin(),
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
	actionNames := map[string]string{"advance": eventAdvance, prefix + "enqueue": eventEnqueue, prefix + "start": eventBegin, prefix + "fire": eventFire, prefix + "success": eventFinish, prefix + "error": eventFinishError, prefix + "requeue": eventFinishRequeue, prefix + "after": eventFinishAfter, prefix + "errorAfter": eventFinishErrorAfter}
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
		case eventFinish, eventFinishError, eventFinishRequeue, eventFinishAfter, eventFinishErrorAfter:
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
		updates := map[string]expr.Expression{adaptergen.CachedVariable(o.version): expr.Expression(o.version)}
		for _, v := range o.values {
			updates[adaptergen.CachedVariable(v)] = expr.Expression(v)
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
	eventFinish:           process.OutcomeSuccess,
	eventFinishError:      process.OutcomeError,
	eventFinishRequeue:    process.OutcomeRequeue,
	eventFinishAfter:      process.OutcomeAfter,
	eventFinishErrorAfter: process.OutcomeErrorAfter,
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

// Package watchmodel generates the event and delivery part of a transition
// model from IR watch rules. Each watched resource gets an event slot that
// other actions fill with Emit; a deliver action passes the event through
// the watch rules of that resource and clears the slot.
//
// The model is bounded: a watched resource has exactly one object, and
// events on that object coalesce. A create followed by an update before
// delivery keeps the create and marks a further update as pending.
// Coalesced updates compare the last delivered object with the current one
// and lose intermediate values, as an informer may after a relist.
package watchmodel

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/model"
)

// Event kinds held in an event slot. The empty string means no event.
const (
	Create = "create"
	Update = "update"
	Delete = "delete"
)

// Add returns spec extended with the event state and delivery actions for
// watches. All watches must belong to one controller. objects lists the
// objects of the watched resources, one per resource; keys lists the
// reconcile keys of the controller. Options supply the object facts that
// mappings read. Add does not change spec in place. Unsupported input
// returns a *goflow.Diagnostic located at the watch.
func Add(spec model.Spec, watches []ir.Watch, objects []ir.ObjectRef, keys []ir.ObjectRef, opts ...Option) (model.Spec, error) {
	g, err := newGenerator(spec, watches, objects, keys)
	if err != nil {
		return spec, err
	}
	for _, opt := range opts {
		opt(g)
	}
	return g.generate()
}

// Option supplies object facts to Add.
type Option func(*generator)

// WithFields supplies field values per object, keyed by the dotted Go field
// path of a field mapping, such as "Spec.Target". Each value becomes the
// initial value of a string state variable (see FieldVar) that other actions
// may change. A reference mapping reads the fields of its dependent objects
// here too, keyed by the dotted dependent path, as fixed values.
func WithFields(fields map[ir.ObjectRef]map[string]string) Option {
	return func(g *generator) { g.fields = fields }
}

// EventVar names the state that holds the undelivered event of resource.
func EventVar(resource ir.GVK) string { return lowerFirst(resource.Kind) + "Event" }

// PendingVar names the state that marks a further update waiting behind the
// current event of resource.
func PendingVar(resource ir.GVK) string { return lowerFirst(resource.Kind) + "EventPending" }

// DeliverAction names the action that delivers the event of resource.
func DeliverAction(resource ir.GVK) string { return "deliver" + resource.Kind }

// Emit returns the updates that record an event of the given kind on the
// object of resource. Callers merge them into the action that changes the
// object. An update behind an undelivered create stays pending; an update
// behind an undelivered update or delete coalesces with it.
//
// Every event moves the resource version. A create starts a new
// generation; an update moves the generation only when generation is true,
// as a spec change does and a status change does not.
func Emit(resource ir.GVK, event string, generation bool) (map[string]expr.Expression, error) {
	ev, pending := EventVar(resource), PendingVar(resource)
	gen, rv := GenerationVar(resource), ResourceVersionVar(resource)
	u := map[string]expr.Expression{rv: expr.Expression(rv + " + 1")}
	switch event {
	case Create:
		u[ev], u[pending] = `"create"`, "false"
		u[gen] = expr.Expression(gen + " + 1")
		u[seen(gen)], u[seen(rv)] = u[gen], u[rv]
	case Delete:
		u[ev], u[pending] = `"delete"`, "false"
	case Update:
		u[ev] = expr.Expression(fmt.Sprintf(`%s == "" ? "update" : %s`, ev, ev))
		u[pending] = expr.Expression(fmt.Sprintf(`%s || %s == "create"`, pending, ev))
		if generation {
			u[gen] = expr.Expression(gen + " + 1")
		}
	default:
		return nil, fmt.Errorf("unknown event kind %q; expected create, update or delete", event)
	}
	return u, nil
}

// resource is one watched resource with its object and rules.
type resource struct {
	gvk    ir.GVK
	object ir.ObjectRef
	rules  []ir.Watch
	// fieldVars lists the field variables that rules on this resource read.
	fieldVars []string
}

type generator struct {
	spec      model.Spec
	resources []*resource
	keys      []ir.ObjectRef
	queued    map[request]string // declared key -> its queued variable
	fields    map[ir.ObjectRef]map[string]string
	declared  map[string]bool // state variables Add declared
	owners    map[ir.ObjectRef][]OwnerRef
	labels    map[ir.ObjectRef]map[string]string
}

var boolFalse = model.Variable{Type: "bool", Initial: "false"}

func newGenerator(spec model.Spec, watches []ir.Watch, objects, keys []ir.ObjectRef) (*generator, error) {
	if len(watches) == 0 {
		return nil, fmt.Errorf("watchmodel: no watches")
	}
	g := &generator{spec: clone(spec), keys: keys, declared: map[string]bool{}}
	byGVK := map[ir.GVK]*resource{}
	byKind := map[string]ir.GVK{}
	for _, w := range watches {
		if err := w.Validate(); err != nil {
			return nil, diagnose(w, "watch", err.Error())
		}
		if w.Controller != watches[0].Controller {
			return nil, diagnose(w, "watch", fmt.Sprintf("watch of controller %q mixed with controller %q; generate one controller at a time", w.Controller, watches[0].Controller))
		}
		if !identifier(w.Resource.Kind) {
			return nil, diagnose(w, "resource", fmt.Sprintf("kind %q is not a model identifier", w.Resource.Kind))
		}
		if other, ok := byKind[w.Resource.Kind]; ok && other != w.Resource {
			return nil, diagnose(w, "resource", fmt.Sprintf("kind %s is watched in two groups (%s and %s)", w.Resource.Kind, other, w.Resource))
		}
		byKind[w.Resource.Kind] = w.Resource
		r, ok := byGVK[w.Resource]
		if !ok {
			r = &resource{gvk: w.Resource}
			byGVK[w.Resource] = r
			g.resources = append(g.resources, r)
		}
		r.rules = append(r.rules, w)
	}
	for _, r := range g.resources {
		found := 0
		for _, o := range objects {
			if o.GVK == r.gvk {
				r.object = o
				found++
			}
		}
		if found != 1 {
			return nil, diagnose(r.rules[0], "object", fmt.Sprintf("watched resource %s needs exactly one declared object, found %d", r.gvk, found))
		}
	}
	return g, nil
}

func (g *generator) generate() (model.Spec, error) {
	if err := g.declareKeys(g.resources[0].rules[0]); err != nil {
		return g.spec, err
	}
	for _, r := range g.resources {
		if err := g.addState(r.rules[0], EventVar(r.gvk), model.Variable{Type: "string", Initial: `""`}); err != nil {
			return g.spec, err
		}
		if err := g.addState(r.rules[0], PendingVar(r.gvk), boolFalse); err != nil {
			return g.spec, err
		}
		for _, v := range []string{GenerationVar(r.gvk), ResourceVersionVar(r.gvk)} {
			for _, name := range []string{v, seen(v)} {
				if err := g.addState(r.rules[0], name, model.Variable{Type: "int", Initial: "0"}); err != nil {
					return g.spec, err
				}
			}
		}
	}
	for _, r := range g.resources {
		action, err := g.deliver(r)
		if err != nil {
			return g.spec, err
		}
		for _, a := range g.spec.Actions {
			if a.Name == action.Name {
				return g.spec, diagnose(r.rules[0], "action", fmt.Sprintf("action %s already exists in the model", a.Name))
			}
		}
		g.spec.Actions = append(g.spec.Actions, action)
	}
	return g.spec, nil
}

// deliver builds the delivery action of r. One action serves every rule on
// r, because the informer hands one event to all handlers of a resource. It
// sets the queued flag of each key a passing rule maps the event to, and
// the wrong-key flag for keys outside the set. It clears the event, or
// moves a pending update into the slot.
func (g *generator) deliver(r *resource) (model.Action, error) {
	ev, pending := EventVar(r.gvk), PendingVar(r.gvk)
	updates := map[string]expr.Expression{
		ev:      expr.Expression(pending + ` ? "update" : ""`),
		pending: "false",
	}
	hits := map[string][]string{}
	var foreign []string
	for _, w := range r.rules {
		if w.Mapping.Kind == ir.MappingCustom {
			return model.Action{}, diagnose(w, "mapping", "custom key mapping has no summary")
		}
		pass, err := passes(r, w)
		if err != nil {
			return model.Action{}, err
		}
		t, err := g.route(r, w)
		if err != nil {
			return model.Action{}, err
		}
		for v, conditions := range t.hits {
			for _, c := range conditions {
				hits[v] = append(hits[v], both(pass, c))
			}
		}
		for _, c := range t.foreign {
			foreign = append(foreign, both(pass, c))
		}
	}
	// The delivered object becomes the ObjectOld of the next update. A
	// create leaves the copy that Emit took at creation, so that an update
	// pending behind it compares against the created object. Field copies
	// then keep the value from before the create, which can add an old
	// target but never drops one.
	for _, v := range append([]string{GenerationVar(r.gvk), ResourceVersionVar(r.gvk)}, r.fieldVars...) {
		updates[seen(v)] = expr.Expression(fmt.Sprintf(`%s == "create" ? %s : %s`, ev, seen(v), v))
	}
	for v, conditions := range hits {
		updates[v] = raise(v, conditions)
	}
	if len(foreign) > 0 {
		updates[WrongKeyVar] = raise(WrongKeyVar, foreign)
	}
	return model.Action{Name: DeliverAction(r.gvk), Guard: expr.Expression(ev + ` != ""`), Updates: updates}, nil
}

// both conjoins two conditions, dropping a literal true.
func both(a, b string) string {
	switch {
	case a == "true":
		return b
	case b == "true":
		return a
	}
	return "(" + a + ") && (" + b + ")"
}

// raise sets flag when any condition holds and keeps it set otherwise.
func raise(flag string, conditions []string) expr.Expression {
	for _, c := range conditions {
		if c == "true" {
			return "true"
		}
	}
	return expr.Expression(flag + " || (" + strings.Join(conditions, ") || (") + ")")
}

// addState declares a new variable and refuses to replace an existing one.
func (g *generator) addState(at ir.Watch, name string, v model.Variable) error {
	if _, ok := g.spec.State[name]; ok {
		return diagnose(at, "state", fmt.Sprintf("state %s already exists in the model", name))
	}
	g.spec.State[name] = v
	return nil
}

// diagnose locates a failure at the source of watch w.
func diagnose(w ir.Watch, construct, reason string) error {
	return &goflow.Diagnostic{File: w.Provenance.Source.File, Line: w.Provenance.Source.Line, Construct: construct, Reason: reason, Source: w.Resource.String()}
}

// clone copies the parts of spec that Add changes.
func clone(spec model.Spec) model.Spec {
	state := make(map[string]model.Variable, len(spec.State))
	for k, v := range spec.State {
		state[k] = v
	}
	spec.State = state
	spec.Actions = append([]model.Action(nil), spec.Actions...)
	return spec
}

func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToLower(r[0])
	}
	return string(r)
}

// identifier reports whether s is an ASCII model identifier.
func identifier(s string) bool {
	for i, c := range s {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || c != '_' && (c < '0' || c > '9')) {
			return false
		}
	}
	return s != ""
}

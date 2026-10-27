package watchmodel

import (
	"fmt"
	"strconv"
	"strings"

	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/model"
)

// WrongKeyVar names the state that records a delivery to a key outside the
// declared key set. A model that reaches it left its bounded domain.
const WrongKeyVar = "wrongKey"

// KeyID derives a model identifier from the namespace and name of key.
// Characters outside [A-Za-z0-9_] become underscores.
func KeyID(key ir.ObjectRef) string {
	parts := []string{key.Namespace, key.Name}
	if key.Cluster != "" {
		parts = append([]string{string(key.Cluster)}, parts...)
	}
	return strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' {
			return c
		}
		return '_'
	}, strings.Join(parts, "_"))
}

// QueuedVar names the state that marks key as queued for reconciliation.
func QueuedVar(key ir.ObjectRef) string { return "queued_" + KeyID(key) }

// request is a reconcile request: controller-runtime keys carry no kind.
type request struct {
	cluster         ir.ClusterID
	namespace, name string
}

func requestOf(r ir.ObjectRef) request { return request{r.Cluster, r.Namespace, r.Name} }

// target is the result of mapping one event through one rule. hits maps a
// key's queued variable to the condition under which the rule enqueues it;
// foreign is the condition under which it enqueues a key outside the set.
type target struct {
	hits    map[string][]string
	foreign []string
}

func (t *target) hit(queued, condition string) {
	if t.hits == nil {
		t.hits = map[string][]string{}
	}
	t.hits[queued] = append(t.hits[queued], condition)
}

// enqueue routes a fixed request: to its key when declared, else foreign.
func (g *generator) enqueue(t *target, req request, condition string) {
	if v, ok := g.queued[req]; ok {
		t.hit(v, condition)
		return
	}
	t.foreign = append(t.foreign, condition)
}

// route maps the event of r through rule w.
func (g *generator) route(r *resource, w ir.Watch) (target, error) {
	var t target
	switch w.Mapping.Kind {
	case ir.MappingSelf:
		g.enqueue(&t, requestOf(r.object), "true")
		return t, nil
	case ir.MappingField:
		return t, g.routeField(&t, r, w)
	case ir.MappingOwner:
		g.routeOwner(&t, r, w)
		return t, nil
	}
	return t, diagnose(w, "mapping", fmt.Sprintf("%s key mapping is not modelled", w.Mapping.Kind))
}

// OwnerRef is an owner reference of an object. Controller marks the
// controlling owner.
type OwnerRef struct {
	Ref        ir.ObjectRef
	Controller bool
}

// WithOwners supplies the owner references of each object.
func WithOwners(owners map[ir.ObjectRef][]OwnerRef) Option {
	return func(g *generator) { g.owners = owners }
}

// routeOwner enqueues each owner whose group and kind match the mapping, as
// controller-runtime's owner handler does; the version is not compared. An
// owner without a namespace takes the object's namespace. With
// OnlyController, owners that are not the controller are ignored.
func (g *generator) routeOwner(t *target, r *resource, w ir.Watch) {
	for _, o := range g.owners[r.object] {
		if o.Ref.GVK.Group != w.Mapping.OwnerGVK.Group || o.Ref.GVK.Kind != w.Mapping.OwnerGVK.Kind {
			continue
		}
		if w.Mapping.OnlyController && !o.Controller {
			continue
		}
		req := request{cluster: r.object.Cluster, namespace: o.Ref.Namespace, name: o.Ref.Name}
		if req.namespace == "" {
			req.namespace = r.object.Namespace
		}
		g.enqueue(t, req, "true")
	}
}

// FieldVar names the state that holds the value of a field-mapping path on
// the object of resource.
func FieldVar(resource ir.GVK, path []string) string {
	return lowerFirst(resource.Kind) + "_" + strings.Join(path, "_")
}

// routeField maps through a field reference. The request takes the object's
// namespace and the field value as its name; an empty value enqueues
// nothing, as in the summarized mapper. A field the caller did not supply
// is unresolved and raises wrongKey. The object's own Name needs no entry.
func (g *generator) routeField(t *target, r *resource, w ir.Watch) error {
	for _, part := range w.Mapping.FieldPath {
		if !identifier(part) {
			return diagnose(w, "mapping", fmt.Sprintf("field path element %q is not a model identifier", part))
		}
	}
	path := strings.Join(w.Mapping.FieldPath, ".")
	value, ok := g.fields[r.object][path]
	if !ok {
		if path == "Name" {
			g.enqueue(t, requestOf(r.object), "true")
		} else {
			t.foreign = append(t.foreign, "true")
		}
		return nil
	}
	v := FieldVar(r.gvk, w.Mapping.FieldPath)
	if !g.declared[v] {
		if err := g.addState(w, v, model.Variable{Type: "string", Initial: expr.Expression(strconv.Quote(value))}); err != nil {
			return err
		}
		g.declared[v] = true
	}
	g.routeValue(t, r.object, v)
	return nil
}

// routeValue routes a request named by the string variable v in the
// namespace of object: to each declared key the value can name, and to
// wrongKey for any other non-empty value.
func (g *generator) routeValue(t *target, object ir.ObjectRef, v string) {
	names := []string{}
	for _, k := range g.keys {
		if k.Cluster == object.Cluster && k.Namespace == object.Namespace {
			match := v + " == " + strconv.Quote(k.Name)
			t.hit(g.queued[requestOf(k)], match)
			names = append(names, match)
		}
	}
	foreign := v + ` != ""`
	if len(names) > 0 {
		foreign += " && !(" + strings.Join(names, " || ") + ")"
	}
	t.foreign = append(t.foreign, foreign)
}

// declareKeys adds the queued flag of every key and the wrong-key flag.
func (g *generator) declareKeys(at ir.Watch) error {
	g.queued = map[request]string{}
	ids := map[string]ir.ObjectRef{}
	for _, k := range g.keys {
		if k.Name == "" {
			return diagnose(at, "key", fmt.Sprintf("key %s has no name", k))
		}
		if other, ok := ids[KeyID(k)]; ok {
			return diagnose(at, "key", fmt.Sprintf("keys %s and %s share the model identifier %s", other, k, KeyID(k)))
		}
		ids[KeyID(k)] = k
		g.queued[requestOf(k)] = QueuedVar(k)
		if err := g.addState(at, QueuedVar(k), boolFalse); err != nil {
			return err
		}
	}
	return g.addState(at, WrongKeyVar, boolFalse)
}

package watchmodel

import (
	"fmt"
	"strings"

	"generative-architecture/pkg/ir"
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
	}
	return t, diagnose(w, "mapping", fmt.Sprintf("%s key mapping is not modelled", w.Mapping.Kind))
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

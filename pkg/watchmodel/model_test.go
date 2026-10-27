package watchmodel_test

import (
	"strings"
	"testing"

	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/watchmodel"
)

var (
	primary   = ir.GVK{Group: "example.test", Version: "v1", Kind: "Parent"}
	secondary = ir.GVK{Group: "example.test", Version: "v1", Kind: "Child"}
)

func watch(resource ir.GVK, mapping ir.KeyMapping, predicates ...string) ir.Watch {
	w := ir.Watch{
		Controller: "parent",
		Resource:   resource,
		Mapping:    mapping,
		Evidence:   ir.Extracted,
		Provenance: ir.Provenance{Generator: "test", Source: ir.Source{File: "setup.go", Line: 7, SHA256: "x"}},
	}
	for _, p := range predicates {
		pred := ir.Predicate{Kind: p}
		if i := strings.IndexByte(p, '('); i > 0 {
			pred = ir.Predicate{Kind: p[:i], Args: map[string]string{"expression": p}}
		}
		w.Predicates = append(w.Predicates, pred)
	}
	w.ID = w.StableID()
	return w
}

func object(resource ir.GVK, name string) ir.ObjectRef {
	return ir.ObjectRef{GVK: resource, Namespace: "ns", Name: name}
}

// baseSpec has one emitting action per resource and event kind, named
// <event><Kind>, for example createChild.
func baseSpec(t *testing.T, resources ...ir.GVK) model.Spec {
	t.Helper()
	spec := model.Spec{Name: "Watches", State: map[string]model.Variable{"steps": {Type: "int", Initial: "0"}}}
	for _, r := range resources {
		for _, ev := range []string{watchmodel.Create, watchmodel.Update, watchmodel.Delete} {
			u, err := watchmodel.Emit(r, ev)
			if err != nil {
				t.Fatal(err)
			}
			u["steps"] = "steps + 1"
			spec.Actions = append(spec.Actions, model.Action{Name: ev + r.Kind, Guard: "true", Updates: u})
		}
	}
	return spec
}

// run compiles spec and applies actions from the initial state.
func run(t *testing.T, spec model.Spec, actions ...string) map[string]any {
	t.Helper()
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions {
		if state, err = m.Next(state, a); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	return state
}

func check(t *testing.T, state map[string]any, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if state[k] != v {
			t.Errorf("%s = %v, want %v (state %v)", k, state[k], v, state)
		}
	}
}

func TestAddEventStateAndDelivery(t *testing.T) {
	watches := []ir.Watch{watch(primary, ir.KeyMapping{Kind: ir.MappingSelf}), watch(secondary, ir.KeyMapping{Kind: ir.MappingSelf})}
	objects := []ir.ObjectRef{object(primary, "p"), object(secondary, "c")}
	spec, err := watchmodel.Add(baseSpec(t, primary, secondary), watches, objects, []ir.ObjectRef{object(primary, "p")})
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"var parentEvent: str", "var childEventPending: bool", "action deliverParent", "action deliverChild"} {
		if !strings.Contains(m.Quint(), want) {
			t.Errorf("generated Quint lacks %q", want)
		}
	}
	for _, tc := range []struct {
		name    string
		actions []string
		want    map[string]any
	}{
		{"initial", nil, map[string]any{"parentEvent": "", "childEvent": "", "childEventPending": false}},
		{"create", []string{"createChild"}, map[string]any{"childEvent": "create", "parentEvent": ""}},
		{"create delivered", []string{"createChild", "deliverChild"}, map[string]any{"childEvent": ""}},
		{"update coalesces", []string{"updateParent", "updateParent"}, map[string]any{"parentEvent": "update", "parentEventPending": false}},
		{"update behind create", []string{"createChild", "updateChild"}, map[string]any{"childEvent": "create", "childEventPending": true}},
		{"pending update follows", []string{"createChild", "updateChild", "deliverChild"}, map[string]any{"childEvent": "update", "childEventPending": false}},
		{"both delivered", []string{"createChild", "updateChild", "deliverChild", "deliverChild"}, map[string]any{"childEvent": ""}},
		{"delete replaces", []string{"createParent", "updateParent", "deleteParent"}, map[string]any{"parentEvent": "delete", "parentEventPending": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check(t, run(t, spec, tc.actions...), tc.want)
		})
	}
	if _, err := m.Next(map[string]any{"steps": int64(0), "parentEvent": "", "parentEventPending": false, "childEvent": "", "childEventPending": false}, "deliverParent"); err == nil {
		t.Error("delivery enabled without an event")
	}
}

func TestAddFailsClosed(t *testing.T) {
	self := ir.KeyMapping{Kind: ir.MappingSelf}
	objects := []ir.ObjectRef{object(primary, "p"), object(secondary, "c")}
	other := watch(secondary, self)
	other.Controller = "other"
	clash := baseSpec(t, primary)
	clash.State["parentEvent"] = model.Variable{Type: "string", Initial: `""`}
	for _, tc := range []struct {
		name    string
		spec    model.Spec
		watches []ir.Watch
		objects []ir.ObjectRef
		want    string
	}{
		{"custom mapping", baseSpec(t, primary), []ir.Watch{watch(primary, ir.KeyMapping{Kind: ir.MappingCustom})}, objects, "custom key mapping"},
		{"two controllers", baseSpec(t, primary, secondary), []ir.Watch{watch(primary, self), other}, objects, "mixed with controller"},
		{"no object", baseSpec(t, primary), []ir.Watch{watch(primary, self)}, nil, "exactly one declared object"},
		{"two objects", baseSpec(t, primary), []ir.Watch{watch(primary, self)}, []ir.ObjectRef{object(primary, "a"), object(primary, "b")}, "exactly one declared object"},
		{"state clash", clash, []ir.Watch{watch(primary, self)}, objects, "already exists"},
		{"same kind in two groups", baseSpec(t, primary), []ir.Watch{watch(primary, self), watch(ir.GVK{Group: "other.test", Version: "v1", Kind: "Parent"}, self)}, objects, "two groups"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := watchmodel.Add(tc.spec, tc.watches, tc.objects, []ir.ObjectRef{object(primary, "p")})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			d, ok := goflow.AsDiagnostic(err)
			if !ok || d.File != "setup.go" || d.Line != 7 {
				t.Fatalf("diagnostic not located: %#v", err)
			}
		})
	}
}

func TestEmitRejectsUnknownEvent(t *testing.T) {
	if _, err := watchmodel.Emit(primary, "generic"); err == nil {
		t.Fatal("accepted unknown event kind")
	}
}

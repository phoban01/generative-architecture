package watchmodel_test

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/watchmodel"
)

// predicateSpec watches the child through a field reference to the key,
// filtered by predicates. consume models a reconcile that takes the key.
func predicateSpec(t *testing.T, predicates ...string) model.Spec {
	t.Helper()
	child := object(secondary, "c")
	ref := ir.KeyMapping{Kind: ir.MappingField, FieldPath: []string{"Spec", "ParentName"}}
	fields := map[ir.ObjectRef]map[string]string{child: {"Spec.ParentName": "p"}}
	spec, err := watchmodel.Add(baseSpec(t, secondary), []ir.Watch{watch(secondary, ref, predicates...)}, []ir.ObjectRef{child}, []ir.ObjectRef{key}, watchmodel.WithFields(fields))
	if err != nil {
		t.Fatal(err)
	}
	queued := watchmodel.QueuedVar(key)
	spec.Actions = append(spec.Actions, model.Action{Name: "consume", Guard: expr.Expression(queued), Updates: map[string]expr.Expression{queued: "false"}})
	return spec
}

// TestPredicates mirrors the status-update wake-up check of the sample
// controller at model level. The creation event is consumed first, so an
// old queued key cannot mask a missing wake-up from a later update.
func TestPredicates(t *testing.T) {
	queued := watchmodel.QueuedVar(key)
	created := []string{"createChild", "deliverChild", "consume"}
	for _, tc := range []struct {
		name       string
		predicates []string
		actions    []string
		want       bool
	}{
		{"resource version, status update", []string{"resourceVersionChanged"}, append(created, "updateChild", "deliverChild"), true},
		{"generation, status update", []string{"generationChanged"}, append(created, "updateChild", "deliverChild"), false},
		{"generation, spec update", []string{"generationChanged"}, append(created, "specChild", "deliverChild"), true},
		{"generation, create", []string{"generationChanged"}, []string{"createChild", "deliverChild"}, true},
		{"generation, delete", []string{"generationChanged"}, append(created, "deleteChild", "deliverChild"), true},
		{"generation, status update behind create", []string{"generationChanged"}, []string{"createChild", "updateChild", "deliverChild", "consume", "deliverChild"}, false},
		{"generation, spec update behind create", []string{"generationChanged"}, []string{"createChild", "specChild", "deliverChild", "consume", "deliverChild"}, true},
		{"generation, coalesced spec and status", []string{"generationChanged"}, append(created, "specChild", "updateChild", "deliverChild"), true},
		{"generation, redelivered state", []string{"generationChanged"}, append(created, "specChild", "deliverChild", "consume", "updateChild", "deliverChild"), false},
		{"both, status update", []string{"generationChanged", "resourceVersionChanged"}, append(created, "updateChild", "deliverChild"), false},
		{"or, status update", []string{"or(generationChanged, resourceVersionChanged)"}, append(created, "updateChild", "deliverChild"), true},
		{"and, status update", []string{"and(generationChanged, resourceVersionChanged)"}, append(created, "updateChild", "deliverChild"), false},
		{"not, status update", []string{"not(generationChanged)"}, []string{"createChild", "deliverChild", "updateChild", "deliverChild"}, true},
		{"not, create", []string{"not(generationChanged)"}, []string{"createChild", "deliverChild"}, false},
		{"nested", []string{"or(generationChanged, and(resourceVersionChanged, not(generationChanged)))"}, append(created, "updateChild", "deliverChild"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check(t, run(t, predicateSpec(t, tc.predicates...), tc.actions...), map[string]any{queued: tc.want})
		})
	}
}

func TestPredicatesFailClosed(t *testing.T) {
	ref := ir.KeyMapping{Kind: ir.MappingSelf}
	for _, tc := range []struct {
		name      string
		predicate ir.Predicate
		want      string
	}{
		{"label filter", ir.Predicate{Kind: "labelChanged"}, "labelChanged is not modelled"},
		{"label inside or", ir.Predicate{Kind: "or", Args: map[string]string{"expression": "or(generationChanged, labelChanged)"}}, "labelChanged is not modelled"},
		{"composite without expression", ir.Predicate{Kind: "and"}, "no expression"},
		{"unknown operator", ir.Predicate{Kind: "xor", Args: map[string]string{"expression": "xor(generationChanged)"}}, "xor"},
		{"not with two arguments", ir.Predicate{Kind: "not", Args: map[string]string{"expression": "not(generationChanged, resourceVersionChanged)"}}, "one argument"},
		{"unterminated", ir.Predicate{Kind: "or", Args: map[string]string{"expression": "or(generationChanged"}}, "unterminated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := watch(primary, ref)
			w.Predicates = []ir.Predicate{tc.predicate}
			_, err := watchmodel.Add(baseSpec(t, primary), []ir.Watch{w}, []ir.ObjectRef{key}, []ir.ObjectRef{key})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if _, ok := goflow.AsDiagnostic(err); !ok {
				t.Fatalf("diagnostic not located: %v", err)
			}
		})
	}
}

package watchmodel_test

import (
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/watchmodel"
)

// TestListMappings routes an event on the Child object c to the Parent
// keys whose label or field names c.
func TestListMappings(t *testing.T) {
	a, b := object(primary, "a"), object(primary, "b")
	qa, qb := watchmodel.QueuedVar(a), watchmodel.QueuedVar(b)
	child := object(secondary, "c")
	stranger := object(primary, "stranger")
	elsewhere := ir.ObjectRef{GVK: primary, Namespace: "other", Name: "a"}
	otherKind := object(ir.GVK{Group: "other.test", Version: "v1", Kind: "Parent"}, "a")
	label := ir.KeyMapping{Kind: ir.MappingLabel, Label: "example.test/child", Dependent: &primary}
	reference := ir.KeyMapping{Kind: ir.MappingReference, DependentPath: []string{"Spec", "Child"}, Dependent: &primary}
	labelled := func(v string) map[string]string { return map[string]string{"example.test/child": v} }
	field := func(v string) map[string]string { return map[string]string{"Spec.Child": v} }
	for _, tc := range []struct {
		name    string
		mapping ir.KeyMapping
		opt     watchmodel.Option
		want    map[string]any
	}{
		{"label selects one", label, watchmodel.WithLabels(map[ir.ObjectRef]map[string]string{a: labelled("c"), b: labelled("d")}),
			map[string]any{qa: true, qb: false, "wrongKey": false}},
		{"label selects both", label, watchmodel.WithLabels(map[ir.ObjectRef]map[string]string{a: labelled("c"), b: labelled("c")}),
			map[string]any{qa: true, qb: true, "wrongKey": false}},
		{"other version still matches", label, watchmodel.WithLabels(map[ir.ObjectRef]map[string]string{{GVK: ir.GVK{Group: primary.Group, Version: "v2", Kind: primary.Kind}, Namespace: "ns", Name: "a"}: labelled("c")}),
			map[string]any{qa: true, qb: false, "wrongKey": false}},
		{"no labels", label, watchmodel.WithLabels(nil),
			map[string]any{qa: false, qb: false, "wrongKey": false}},
		{"label key absent", label, watchmodel.WithLabels(map[ir.ObjectRef]map[string]string{a: {"other": "c"}}),
			map[string]any{qa: false, qb: false, "wrongKey": false}},
		{"other namespace ignored", label, watchmodel.WithLabels(map[ir.ObjectRef]map[string]string{elsewhere: labelled("c")}),
			map[string]any{qa: false, qb: false, "wrongKey": false}},
		{"other group ignored", label, watchmodel.WithLabels(map[ir.ObjectRef]map[string]string{otherKind: labelled("c")}),
			map[string]any{qa: false, qb: false, "wrongKey": false}},
		{"undeclared dependent is a wrong key", label, watchmodel.WithLabels(map[ir.ObjectRef]map[string]string{stranger: labelled("c")}),
			map[string]any{qa: false, qb: false, "wrongKey": true}},
		{"reference selects one", reference, watchmodel.WithFields(map[ir.ObjectRef]map[string]string{a: field("d"), b: field("c")}),
			map[string]any{qa: false, qb: true, "wrongKey": false}},
		{"reference ignores labels", reference, watchmodel.WithLabels(map[ir.ObjectRef]map[string]string{a: labelled("c")}),
			map[string]any{qa: false, qb: false, "wrongKey": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := watchmodel.Add(baseSpec(t, secondary), []ir.Watch{watch(secondary, tc.mapping)}, []ir.ObjectRef{child}, []ir.ObjectRef{a, b}, tc.opt)
			if err != nil {
				t.Fatal(err)
			}
			check(t, run(t, spec, "createChild", "deliverChild"), tc.want)
		})
	}
}

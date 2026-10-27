package watchmodel_test

import (
	"strings"
	"testing"

	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/watchmodel"
)

var key = object(primary, "p")

func TestFieldMapping(t *testing.T) {
	queued := watchmodel.QueuedVar(key)
	child := object(secondary, "c")
	ref := ir.KeyMapping{Kind: ir.MappingField, FieldPath: []string{"Spec", "ParentName"}}
	field := watchmodel.FieldVar(secondary, ref.FieldPath)
	for _, tc := range []struct {
		name    string
		mapping ir.KeyMapping
		object  ir.ObjectRef
		fields  map[string]string
		actions []string
		want    map[string]any
	}{
		{"declared key", ref, child, map[string]string{"Spec.ParentName": "p"}, []string{"createChild", "deliverChild"}, map[string]any{queued: true, "wrongKey": false, field: "p"}},
		{"elsewhere", ref, child, map[string]string{"Spec.ParentName": "other"}, []string{"createChild", "deliverChild"}, map[string]any{queued: false, "wrongKey": true}},
		{"empty value", ref, child, map[string]string{"Spec.ParentName": ""}, []string{"createChild", "deliverChild"}, map[string]any{queued: false, "wrongKey": false}},
		{"value set by an action", ref, child, map[string]string{"Spec.ParentName": ""}, []string{"pointChild", "createChild", "deliverChild"}, map[string]any{queued: true, "wrongKey": false}},
		{"unresolved field", ref, child, nil, []string{"createChild", "deliverChild"}, map[string]any{queued: false, "wrongKey": true}},
		{"own name elsewhere", ir.KeyMapping{Kind: ir.MappingField, FieldPath: []string{"Name"}}, child, nil, []string{"createChild", "deliverChild"}, map[string]any{queued: false, "wrongKey": true}},
		{"own name at key", ir.KeyMapping{Kind: ir.MappingField, FieldPath: []string{"Name"}}, object(secondary, "p"), nil, []string{"createChild", "deliverChild"}, map[string]any{queued: true, "wrongKey": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[ir.ObjectRef]map[string]string{tc.object: tc.fields}
			spec, err := watchmodel.Add(baseSpec(t, secondary), []ir.Watch{watch(secondary, tc.mapping)}, []ir.ObjectRef{tc.object}, []ir.ObjectRef{key}, watchmodel.WithFields(fields))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := spec.State[field]; ok {
				spec.Actions = append(spec.Actions, model.Action{Name: "pointChild", Guard: "true", Updates: map[string]expr.Expression{field: `"p"`}})
			}
			check(t, run(t, spec, tc.actions...), tc.want)
		})
	}
}

func TestSelfMapping(t *testing.T) {
	queued := watchmodel.QueuedVar(key)
	if queued != "queued_ns_p" {
		t.Fatalf("unexpected queued variable %s", queued)
	}
	self := ir.KeyMapping{Kind: ir.MappingSelf}
	for _, tc := range []struct {
		name    string
		objects []ir.ObjectRef
		actions []string
		want    map[string]any
	}{
		{"declared key", []ir.ObjectRef{key}, []string{"createParent", "deliverParent"}, map[string]any{queued: true, "wrongKey": false}},
		{"foreign object", []ir.ObjectRef{object(primary, "other")}, []string{"createParent", "deliverParent"}, map[string]any{queued: false, "wrongKey": true}},
		{"undelivered", []ir.ObjectRef{key}, []string{"createParent"}, map[string]any{queued: false, "wrongKey": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := watchmodel.Add(baseSpec(t, primary), []ir.Watch{watch(primary, self)}, tc.objects, []ir.ObjectRef{key})
			if err != nil {
				t.Fatal(err)
			}
			check(t, run(t, spec, tc.actions...), tc.want)
		})
	}
}

func TestKeysFailClosed(t *testing.T) {
	self := []ir.Watch{watch(primary, ir.KeyMapping{Kind: ir.MappingSelf})}
	for _, tc := range []struct {
		name string
		keys []ir.ObjectRef
		want string
	}{
		{"unnamed key", []ir.ObjectRef{{GVK: primary, Namespace: "ns"}}, "has no name"},
		{"shared identifier", []ir.ObjectRef{object(primary, "a-b"), object(primary, "a_b")}, "share the model identifier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := watchmodel.Add(baseSpec(t, primary), self, []ir.ObjectRef{key}, tc.keys)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

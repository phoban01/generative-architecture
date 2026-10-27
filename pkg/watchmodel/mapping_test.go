package watchmodel_test

import (
	"strings"
	"testing"

	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/watchmodel"
)

var key = object(primary, "p")

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

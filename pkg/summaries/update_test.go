package summaries

import (
	"testing"

	"generative-architecture/pkg/ir"
)

func TestBuiltinUpdate(t *testing.T) {
	r := Builtin()
	for _, tc := range []struct {
		operation, target, value string
	}{
		{ir.OpUpdate, "$object", "$value"},
		{OperationKey(ir.OpUpdate, "status"), "$status", "$value.status"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			s, err := r.Lookup(tc.operation, ir.ConsistencyDirect)
			if err != nil {
				t.Fatal(err)
			}
			if !equalNames(outcomeNames(s), "ok", "conflict", "notFound", "error") {
				t.Fatalf("outcomes %v", outcomeNames(s))
			}
			ok, conflict, notFound := s.Outcomes[0], s.Outcomes[1], s.Outcomes[2]
			if ok.Guard != "$exists && $resourceVersion == $snapshotVersion" || ok.Updates[tc.target] != tc.value || ok.Updates["$resourceVersion"] != "$resourceVersion + 1" {
				t.Fatalf("ok %+v", ok)
			}
			if conflict.Guard != "$exists && $resourceVersion != $snapshotVersion" || conflict.Error != ErrorConflict {
				t.Fatalf("conflict %+v", conflict)
			}
			if notFound.Guard != "!$exists" || notFound.Error != ErrorNotFound || s.Outcomes[3].Error != ErrorTransient {
				t.Fatalf("notFound %+v error %+v", notFound, s.Outcomes[3])
			}
			current := map[string]any{"exists": true, "object": "", "status": "", "resourceVersion": int64(4), "snapshotVersion": int64(4), "value": map[string]any{"status": "ready"}}
			guardsParse(t, s, current)
			b := bareBindings(s)
			if evalTemplate(t, ok.Guard, b, current) != true || evalTemplate(t, conflict.Guard, b, current) != false {
				t.Fatal("a current copy must update, not conflict")
			}
			stale := map[string]any{"exists": true, "resourceVersion": int64(4), "snapshotVersion": int64(3)}
			if evalTemplate(t, ok.Guard, b, stale) != false || evalTemplate(t, conflict.Guard, b, stale) != true {
				t.Fatal("a stale copy must conflict")
			}
		})
	}
}

package summaries

import (
	"testing"

	"generative-architecture/pkg/ir"
)

func TestBuiltinGet(t *testing.T) {
	r := Builtin()
	for _, tc := range []struct {
		consistency, source string
	}{
		{ir.ConsistencyDirect, "object"},
		{ir.ConsistencyCached, "cache"},
	} {
		t.Run(tc.consistency, func(t *testing.T) {
			s, err := r.Lookup(ir.OpGet, tc.consistency)
			if err != nil {
				t.Fatal(err)
			}
			if !equalNames(outcomeNames(s), "found", "notFound", "error") {
				t.Fatalf("outcomes %v", outcomeNames(s))
			}
			found, notFound, failure := s.Outcomes[0], s.Outcomes[1], s.Outcomes[2]
			if found.Guard != "$exists" || found.Error != "" || found.Updates["$snapshot"] != "$"+tc.source {
				t.Fatalf("found %+v", found)
			}
			if notFound.Guard != "!$exists" || notFound.Error != ErrorNotFound || len(notFound.Updates) != 0 {
				t.Fatalf("notFound %+v", notFound)
			}
			if failure.Guard != "true" || failure.Error != ErrorTransient {
				t.Fatalf("error %+v", failure)
			}
			vars := map[string]any{"exists": true, tc.source: "v", tc.source + "Version": int64(3), "resourceVersion": int64(3), "cacheVersion": int64(3)}
			guardsParse(t, s, vars)
			b := bareBindings(s)
			if v := evalTemplate(t, notFound.Guard, b, vars); v != false {
				t.Fatalf("notFound guard with an existing object: %v", v)
			}
		})
	}
}

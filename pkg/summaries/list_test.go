package summaries

import (
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

// A list binds the caller's list local to the catalog the generator
// lowers: the identities of the existing objects that match the call's
// options. The summary itself filters nothing (ADR-0011).
func TestBuiltinList(t *testing.T) {
	r := Builtin()
	for _, consistency := range []string{ir.ConsistencyDirect, ir.ConsistencyCached} {
		t.Run(consistency, func(t *testing.T) {
			s, err := r.Lookup(ir.OpList, consistency)
			if err != nil {
				t.Fatal(err)
			}
			if !equalNames(outcomeNames(s), "ok", "error") || s.Outcomes[0].Guard != "true" || s.Outcomes[1].Error != ErrorTransient {
				t.Fatalf("outcomes %+v", s.Outcomes)
			}
			if items := s.Outcomes[0].Updates["$items"]; items != "$catalog" || len(s.Outcomes[0].Updates) != 1 {
				t.Fatalf("ok must bind $items to $catalog: %+v", s.Outcomes[0].Updates)
			}
			if !equalNames(s.Bindings, "$items", "$catalog") {
				t.Fatalf("bindings %v", s.Bindings)
			}
			b := bareBindings(s)
			vars := map[string]any{"catalog": []any{"a", "b"}, "items": []any{}}
			guardsParse(t, s, vars)
			if got := evalTemplate(t, "size("+s.Outcomes[0].Updates["$items"]+")", b, vars); got != int64(2) {
				t.Fatalf("items %v", got)
			}
		})
	}
}

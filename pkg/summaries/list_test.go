package summaries

import (
	"testing"

	"generative-architecture/pkg/ir"
)

func TestBuiltinList(t *testing.T) {
	r := Builtin()
	catalog := []any{
		map[string]any{"name": "a", "namespace": "one", "labels": map[string]any{"tier": "x", "app": "p"}},
		map[string]any{"name": "b", "namespace": "one", "labels": map[string]any{"tier": "y"}},
		map[string]any{"name": "c", "namespace": "two", "labels": map[string]any{"tier": "x"}},
	}
	cases := []struct {
		name    string
		options map[string]any
		want    int64
	}{
		{"whole domain", map[string]any{}, 3},
		{"namespace", map[string]any{"namespace": "one"}, 2},
		{"equality selector", map[string]any{"labelSelector": map[string]any{"tier": "x"}}, 2},
		{"namespace and selector", map[string]any{"namespace": "one", "labelSelector": map[string]any{"tier": "x", "app": "p"}}, 1},
		{"missing label", map[string]any{"labelSelector": map[string]any{"app": "q"}}, 0},
	}
	for _, consistency := range []string{ir.ConsistencyDirect, ir.ConsistencyCached} {
		t.Run(consistency, func(t *testing.T) {
			s, err := r.Lookup(ir.OpList, consistency)
			if err != nil {
				t.Fatal(err)
			}
			if !equalNames(outcomeNames(s), "ok", "error") || s.Outcomes[0].Guard != "true" || s.Outcomes[1].Error != ErrorTransient {
				t.Fatalf("outcomes %+v", s.Outcomes)
			}
			items, ok := s.Outcomes[0].Updates["$items"]
			if !ok || len(s.Outcomes[0].Updates) != 1 {
				t.Fatalf("ok must bind only $items: %+v", s.Outcomes[0].Updates)
			}
			b := bareBindings(s)
			for _, c := range cases {
				vars := map[string]any{"catalog": catalog, "items": []any{}, "options": c.options}
				guardsParse(t, s, vars)
				// The result is a finite subset of the catalog.
				if got := evalTemplate(t, "size("+items+")", b, vars); got != c.want {
					t.Fatalf("%s: %d items, want %d", c.name, got, c.want)
				}
			}
		})
	}
}

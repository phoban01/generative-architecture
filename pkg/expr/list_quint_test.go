package expr_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/quint"
)

// TestListDifferentialOverSampledTraces samples a model that grows, reads
// and clears a list of identities, and replays every trace through the
// original CEL: Quint's List and cel-go's list agree on size, membership,
// indexing and concatenation.
func TestListDifferentialOverSampledTraces(t *testing.T) {
	exe := os.Getenv("QUINT_BIN")
	if exe == "" {
		t.Skip("QUINT_BIN unset")
	}
	m, err := model.Compile(model.Spec{
		Name: "Lists",
		State: map[string]model.Variable{
			"items": {Type: expr.ListOfString, Initial: "[]"},
			"count": {Type: "int", Initial: "0"},
			"first": {Type: "string", Initial: `""`},
			"hasB":  {Type: "bool", Initial: "false"},
		},
		Actions: []model.Action{
			{Name: "addA", Guard: `!("a" in items)`, Updates: map[string]expr.Expression{"items": `items + ["a"]`}},
			{Name: "addB", Guard: `!("b" in items)`, Updates: map[string]expr.Expression{"items": `items + ["b"]`}},
			{Name: "observe", Guard: "size(items) > 0", Updates: map[string]expr.Expression{"count": "size(items)", "first": "items[0]", "hasB": `items.exists(x, x == "b")`}},
			{Name: "clear", Guard: "size(items) > 0", Updates: map[string]expr.Expression{"items": "[]"}},
		},
		Properties: []model.Property{{Name: "bounded", Expression: "size(items) <= 2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := quint.Sample(ctx, m, quint.Options{Executable: exe, Samples: 20, Steps: 20, Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := quint.Differential(m, result.Traces); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, trace := range result.Traces {
		for _, s := range trace.States {
			if s.Event == "observe" {
				items := s.Values["items"].([]any)
				if int64(len(items)) != s.Values["count"] || items[0] != s.Values["first"] {
					t.Fatalf("observed %v count %v first %v", items, s.Values["count"], s.Values["first"])
				}
				if s.Values["hasB"] == true {
					seen["b"] = true
				}
				if reflect.DeepEqual(items, []any{"a", "b"}) || reflect.DeepEqual(items, []any{"b", "a"}) {
					seen["two"] = true
				}
			}
		}
	}
	if !seen["b"] || !seen["two"] {
		t.Fatalf("sampling never observed b or a two-element list: %v", seen)
	}
}

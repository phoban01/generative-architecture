package expr_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/quint"
)

// TestConcatenationDifferentialOverSampledTraces samples a model whose
// observe action stores two string concatenations, and replays every trace
// through the original CEL. Each stored value must agree with cel-go, and
// all four values of the conditional one must occur.
func TestConcatenationDifferentialOverSampledTraces(t *testing.T) {
	exe := os.Getenv("QUINT_BIN")
	if exe == "" {
		t.Skip("QUINT_BIN unset")
	}
	m, err := model.Compile(model.Spec{
		Name: "Concatenation",
		State: map[string]model.Variable{
			"a": {Type: "bool", Initial: "false"}, "b": {Type: "bool", Initial: "false"},
			"name": {Type: "string", Initial: `""`}, "secret": {Type: "string", Initial: `""`},
		},
		Actions: []model.Action{
			{Name: "flipA", Guard: "true", Updates: map[string]expr.Expression{"a": "!a"}},
			{Name: "flipB", Guard: "true", Updates: map[string]expr.Expression{"b": "!b"}},
			{Name: "observe", Guard: "true", Updates: map[string]expr.Expression{
				"name":   `"p-" + (a ? "x" : "y") + (b ? "1" : "2")`,
				"secret": `"db" + "-credentials"`,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := quint.Sample(ctx, m, quint.Options{Executable: exe, Samples: 20, Steps: 20, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := quint.Differential(m, result.Traces); err != nil {
		t.Fatal(err)
	}
	seen := map[any]bool{}
	for _, trace := range result.Traces {
		for _, s := range trace.States {
			if s.Event == "observe" {
				seen[s.Values["name"]] = true
				if s.Values["secret"] != "db-credentials" {
					t.Fatalf("secret %v", s.Values["secret"])
				}
			}
		}
	}
	for _, want := range []string{"p-x1", "p-x2", "p-y1", "p-y2"} {
		if !seen[want] {
			t.Errorf("value %s not observed: %v", want, seen)
		}
	}
}

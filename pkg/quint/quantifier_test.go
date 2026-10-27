package quint_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/quint"
)

// on is the CEL predicate "the flag of key v is set" over the keys a and b.
func on(v string) string {
	return strings.ReplaceAll(`((%s == "a" && a) || (%s == "b" && b))`, "%s", v)
}

// quantifierModel declares the domain Keys = {a, b}. Its observe action
// stores the value of exists(), all(), a nested exists() and a membership
// test in state, so every Quint trace records what the Quint lowering
// computed and the differential check recomputes it with cel-go.
func quantifierModel(t *testing.T) *model.Compiled {
	t.Helper()
	m, err := model.Compile(model.Spec{
		Name:    "Quantifiers",
		Domains: map[string][]string{"Keys": {"a", "b"}},
		State: map[string]model.Variable{
			"a": {Type: "bool", Initial: "false"}, "b": {Type: "bool", Initial: "false"},
			"chosen": {Type: "string", Initial: `"a"`},
			"some":   {Type: "bool", Initial: "false"}, "every": {Type: "bool", Initial: "false"},
			"pair": {Type: "bool", Initial: "false"}, "member": {Type: "bool", Initial: "true"},
		},
		Actions: []model.Action{
			{Name: "flipA", Guard: "true", Updates: map[string]expr.Expression{"a": "!a"}},
			{Name: "flipB", Guard: "true", Updates: map[string]expr.Expression{"b": "!b"}},
			{Name: "chooseOutside", Guard: "true", Updates: map[string]expr.Expression{"chosen": `"c"`}},
			{Name: "chooseInside", Guard: "true", Updates: map[string]expr.Expression{"chosen": `"b"`}},
			{Name: "observe", Guard: "true", Updates: map[string]expr.Expression{
				"some":   expr.Expression("Keys.exists(k, " + on("k") + ")"),
				"every":  expr.Expression("Keys.all(k, " + on("k") + ")"),
				"pair":   expr.Expression("Keys.exists(x, Keys.exists(y, x != y && " + on("x") + " && " + on("y") + "))"),
				"member": "Keys.exists(k, k == chosen)",
			}},
		},
		Properties: []model.Property{
			{Name: "pairNeedsSome", Expression: "!pair || some"},
			{Name: "domainHasB", Expression: `Keys.exists(k, k == "b") && !Keys.exists(k, k == "c")`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestQuantifierDifferentialOverSampledTraces samples the model with Quint
// and replays every trace through the original CEL. Each observed value of
// a quantifier must agree, and both outcomes of each must occur.
func TestQuantifierDifferentialOverSampledTraces(t *testing.T) {
	exe := os.Getenv("QUINT_BIN")
	if exe == "" {
		t.Skip("QUINT_BIN unset")
	}
	m := quantifierModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := quint.Sample(ctx, m, quint.Options{Executable: exe, Samples: 20, Steps: 20, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := quint.Differential(m, result.Traces)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Coverage["observe"] == 0 {
		t.Fatalf("no observe step sampled: %v", stats.Coverage)
	}
	outcomes := map[string]map[bool]bool{"some": {}, "every": {}, "pair": {}, "member": {}}
	for _, trace := range result.Traces {
		for _, s := range trace.States {
			if s.Event != "observe" {
				continue
			}
			for name := range outcomes {
				outcomes[name][s.Values[name] == true] = true
			}
		}
	}
	for name, seen := range outcomes {
		if !seen[true] || !seen[false] {
			t.Errorf("%s: the sampled observations do not reach both outcomes: %v", name, seen)
		}
	}
}

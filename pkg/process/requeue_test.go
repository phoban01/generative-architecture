package process_test

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/process"
)

// finish runs a deterministic process model from its initial state until
// done holds and returns the final state.
func finish(t *testing.T, spec model.Spec) map[string]any {
	t.Helper()
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	for steps := 0; state["done"] != true; steps++ {
		if steps > 10 {
			t.Fatal("process failed to terminate")
		}
		selected := ""
		for _, a := range spec.Actions {
			if _, err := m.Next(state, a.Name); err == nil {
				if selected != "" {
					t.Fatal("ambiguous process")
				}
				selected = a.Name
			}
		}
		if selected == "" {
			t.Fatal("stuck process")
		}
		if state, err = m.Next(state, selected); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

// A reconciler that returns Result{RequeueAfter: d} records the requeue on
// its return node, and the process model reports it as the after outcome.
func TestRequeueOutcome(t *testing.T) {
	l, err := goflow.Load("../..", goflow.Selector{Package: "./pkg/goflow/testdata/typed", Receiver: "Reconciler", Method: "RequeueSoon"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := goflow.ExtractTyped(l, goflow.Options{
		Scope:   map[string]expr.Symbol{"ready": {Type: "bool", Quint: "ready"}},
		Returns: []goflow.Return{{Zero: true, Requeue: true}, {Error: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ready expr.Expression
		want  string
	}{
		{"true", process.OutcomeSuccess},
		{"false", process.OutcomeAfter},
	} {
		state := map[string]model.Variable{"ready": {Type: "bool", Initial: tc.ready}, "outcome": {Type: "string", Initial: `""`}}
		spec, err := process.Expand(model.Spec{Name: "Requeue", State: state}, *p, process.Config{PC: "pc", Finished: "done", Outcome: "outcome", Results: []string{"", ""}})
		if err != nil {
			t.Fatal(err)
		}
		if got := finish(t, spec)["outcome"]; got != tc.want {
			t.Errorf("ready=%s: outcome %v, want %s", tc.ready, got, tc.want)
		}
	}
	// Without an outcome variable the requeue would be dropped.
	state := map[string]model.Variable{"ready": {Type: "bool", Initial: "false"}}
	if _, err := process.Expand(model.Spec{Name: "Requeue", State: state}, *p, process.Config{PC: "pc", Finished: "done", Results: []string{"", ""}}); err == nil || !strings.Contains(err.Error(), "no outcome variable") {
		t.Fatalf("requeue dropped: %v", err)
	}
}

// Each requeue request and error result maps to one runtime outcome.
func TestOutcomeOfReturn(t *testing.T) {
	for _, tc := range []struct {
		requeue string
		err     expr.Expression
		failed  bool
		want    string
	}{
		{"", "false", false, process.OutcomeSuccess},
		{"", "failed", true, process.OutcomeError},
		{"", "failed", false, process.OutcomeSuccess},
		{process.OutcomeRequeue, "false", false, process.OutcomeRequeue},
		{process.OutcomeRequeue, "failed", true, process.OutcomeError},
		{process.OutcomeAfter, "", false, process.OutcomeAfter},
		{process.OutcomeAfter, "failed", true, process.OutcomeErrorAfter},
	} {
		p := goflow.Program{Nodes: []goflow.Node{{ID: 0, Kind: "return", Requeue: tc.requeue, ReturnError: tc.err}}}
		state := map[string]model.Variable{"failed": {Type: "bool", Initial: expr.Expression(map[bool]string{true: "true", false: "false"}[tc.failed])}, "outcome": {Type: "string", Initial: `""`}}
		spec, err := process.Expand(model.Spec{Name: "Outcome", State: state}, p, process.Config{PC: "pc", Finished: "done", Outcome: "outcome"})
		if err != nil {
			t.Fatal(err)
		}
		if got := finish(t, spec)["outcome"]; got != tc.want {
			t.Errorf("requeue %q, error %s=%v: outcome %v, want %s", tc.requeue, tc.err, tc.failed, got, tc.want)
		}
	}
}

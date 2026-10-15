package process_test

import (
	"strings"
	"testing"

	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/process"
	"generative-architecture/pkg/summaries"
)

func TestExpandRecordsOutcomeErrors(t *testing.T) {
	p := goflow.Program{Entry: 0, Nodes: []goflow.Node{
		{ID: 0, Kind: "get", Consistency: "direct", Bindings: getBindings, Results: nil, Error: "err", Next: 1},
		{ID: 1, Kind: "return"},
	}}
	state := getState()
	state["err"] = model.Variable{Type: "bool", Initial: "false"}
	state["err_notFound"] = model.Variable{Type: "bool", Initial: "false"}
	spec, err := process.Expand(model.Spec{Name: "Get", State: state}, p, process.Config{PC: "pc", Finished: "done", Summaries: summaries.Builtin()})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"node0_found":    {"false", "false"},
		"node0_notFound": {"true", "true"},
		"node0_error":    {"true", "false"},
	}
	for _, a := range spec.Actions {
		w, ok := want[a.Name]
		if !ok {
			continue
		}
		if string(a.Updates["err"]) != w[0] || string(a.Updates["err_notFound"]) != w[1] {
			t.Fatalf("%s records err=%s err_notFound=%s, want %v", a.Name, a.Updates["err"], a.Updates["err_notFound"], w)
		}
	}
	// An error symbol that is not model state fails closed.
	_, err = process.Expand(model.Spec{Name: "Get", State: getState()}, p, process.Config{PC: "pc", Finished: "done", Summaries: summaries.Builtin()})
	if err == nil || !strings.Contains(err.Error(), "err") {
		t.Fatalf("unrecorded error symbol accepted: %v", err)
	}
}

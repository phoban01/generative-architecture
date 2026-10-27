package process_test

import (
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/process"
	"testing"
)

func TestSubreconcilerReturnAndBranchComposition(t *testing.T) {
	child, err := goflow.Extract([]byte(`package demo
func Check(value string) bool { if value == "" { return false }; return true }`), goflow.Options{Method: "Check", Scope: map[string]expr.Symbol{"value": {Type: "string", Quint: "value"}}, Returns: []goflow.Return{{Type: "bool"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Non-contiguous caller IDs ensure binding uses graph identities, not offsets.
	parent := goflow.Program{Entry: 10, Nodes: []goflow.Node{{ID: 10, Kind: "call", Callee: "check", Next: 20}, {ID: 20, Kind: "return", Results: []expr.Expression{"ok"}}}}
	for _, initial := range []expr.Expression{`""`, `"machine-1"`} {
		p, err := process.Inline(parent, map[string]process.Call{"check": {Program: *child, Bindings: map[string]expr.Expression{"value": "input"}, Results: []string{"ok"}}})
		if err != nil {
			t.Fatal(err)
		}
		spec, err := process.Expand(model.Spec{Name: "CallModel", State: map[string]model.Variable{"input": {Type: "string", Initial: initial}, "ok": {Type: "bool", Initial: "false"}, "answer": {Type: "bool", Initial: "false"}}}, p, process.Config{PC: "pc", Finished: "done", Results: []string{"answer"}})
		if err != nil {
			t.Fatal(err)
		}
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
			state, err = m.Next(state, selected)
			if err != nil {
				t.Fatal(err)
			}
		}
		if state["answer"] != (initial != `""`) {
			t.Fatalf("wrong composed return: %v", state)
		}
	}
	// Recursive/missing call summaries must not silently become no-ops.
	if _, err := process.Inline(parent, nil); err == nil {
		t.Fatal("accepted missing call")
	}
	if _, err := process.Inline(parent, map[string]process.Call{"check": {Program: parent}}); err == nil {
		t.Fatal("accepted recursion")
	}
}
func TestExpandFailsClosed(t *testing.T) {
	p := goflow.Program{Entry: 0, Nodes: []goflow.Node{{ID: 0, Kind: "unknown", Next: 1}, {ID: 1, Kind: "return"}}}
	_, err := process.Expand(model.Spec{Name: "Unknown"}, p, process.Config{PC: "pc", Finished: "done"})
	if err == nil {
		t.Fatal("unknown effect accepted")
	}
}

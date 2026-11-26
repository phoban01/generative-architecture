package goflow

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// A closure bound once to a local extracts like a function declaration:
// its binding adds no node, each call inlines the body with the parameter
// bound to the argument, and its returns assign the call's local.
func TestClosureInlinesAtCallSite(t *testing.T) {
	l := loadFixture(t, "./pkg/goflow/testdata/typed", "Controller", "Budget")
	scope := map[string]expr.Symbol{"attempts": {Type: "int", Quint: "attempts"}, "ready": {Type: "bool", Quint: "ready"}, "left": {Type: "int", Quint: "left"}, "more": {Type: "int", Quint: "more"}}
	p, err := ExtractTyped(l, Options{Scope: scope, Returns: []Return{{Type: "int"}}})
	if err != nil {
		t.Fatal(err)
	}
	entry := p.Nodes[p.Entry]
	if entry.Kind != "branch" || entry.Expression != "(attempts >= 3)" {
		t.Fatalf("the first call's body does not start the program: %+v", entry)
	}
	updates := map[string][]string{}
	branches := 0
	for _, n := range p.Nodes {
		switch n.Kind {
		case "assign":
			for target, v := range n.Updates {
				updates[target] = append(updates[target], string(v))
			}
		case "branch":
			branches++
		}
	}
	for target, want := range map[string][]string{"left": {"0", "(3 - attempts)"}, "more": {"0", "(3 - (attempts + 1))"}} {
		for _, w := range want {
			found := false
			for _, got := range updates[target] {
				if got == w {
					found = true
				}
			}
			if !found {
				t.Errorf("%s is never assigned %s; assignments %v", target, w, updates[target])
			}
		}
	}
	// Two inlined bodies and the ready test: no node for the binding.
	if branches != 3 {
		t.Errorf("%d branches, want 3:\n%+v", branches, p.Nodes)
	}
	for _, n := range p.Nodes {
		if strings.Contains(n.Source, "func(") {
			t.Errorf("the closure binding became a node: %+v", n)
		}
	}
}

// A closure that is reassigned is a diagnostic at its binding.
func TestClosureReassignedIsDiagnostic(t *testing.T) {
	l := loadFixture(t, "./pkg/goflow/testdata/typed", "Controller", "Rebound")
	scope := map[string]expr.Symbol{"attempts": {Type: "int", Quint: "attempts"}, "result": {Type: "int", Quint: "result"}}
	_, err := ExtractTyped(l, Options{Scope: scope, Returns: []Return{{Type: "int"}}})
	d, ok := AsDiagnostic(err)
	if !ok || d.Construct != "closure" || !strings.Contains(d.Reason, "reassigned") {
		t.Fatalf("expected a closure diagnostic, got %v", err)
	}
}

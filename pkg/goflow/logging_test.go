package goflow

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// Logging statements add no node. A call with a possible effect inside a
// log argument is a located diagnostic, because dropping the log would
// drop it.
func TestLoggingHasNoEffect(t *testing.T) {
	opts := Options{
		Scope:   map[string]expr.Symbol{"ready": {Type: "bool", Quint: "ready"}},
		Returns: []Return{{Zero: true}, {Error: true}},
	}
	p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/kube", "Reconciler", "Log"), opts)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, n := range p.Nodes {
		kinds = append(kinds, n.Kind)
	}
	if got := strings.Join(kinds, ","); got != "return,return,branch" || p.Nodes[p.Entry].Kind != "branch" {
		t.Fatalf("nodes %s, want only the branch and its two returns", got)
	}

	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/kube", "Reconciler", "LogCall"), opts)
	d, ok := AsDiagnostic(err)
	if !ok || d.Construct != "call" || d.Line == 0 || !strings.Contains(d.Reason, "Reconciler.owner inside a logging call") {
		t.Fatalf("got %v, want a diagnostic on the call inside the log", err)
	}
}

// A callee the options declare pure may appear inside a log argument: the
// log is dropped with the call (ADR-0012).
func TestDeclaredPureCallInsideLog(t *testing.T) {
	opts := Options{
		Scope:   map[string]expr.Symbol{"ready": {Type: "bool", Quint: "ready"}},
		Returns: []Return{{Zero: true}, {Error: true}},
		Pure:    map[Callee]bool{{Package: "github.com/phoban01/generative-architecture/pkg/goflow/testdata/kube", Receiver: "Reconciler", Method: "owner"}: true},
	}
	p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/kube", "Reconciler", "LogCall"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 1 || p.Nodes[p.Entry].Kind != "return" {
		t.Fatalf("nodes %+v, want only the return", p.Nodes)
	}
}

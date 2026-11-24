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

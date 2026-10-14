package goflow

import (
	"testing"
)

func TestErrorIdiomsLowerToOutcomeSymbols(t *testing.T) {
	opts := fetchOptions()
	opts.Returns = []Return{{Type: "string"}, {Error: true}}
	p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Ensure"), opts)
	if err != nil {
		t.Fatal(err)
	}
	notFound := p.Nodes[p.Nodes[p.Entry].Next]
	if notFound.Kind != "branch" || notFound.Expression != "err_notFound" {
		t.Fatalf("IsNotFound not lowered: %+v", notFound)
	}
	failed := p.Nodes[notFound.Else]
	if failed.Kind != "branch" || failed.Expression != "err" {
		t.Fatalf("err != nil not lowered: %+v", failed)
	}
	if p.Nodes[notFound.Next].Kind != "return" || p.Nodes[notFound.Next].Expression != `"absent"` {
		t.Fatalf("not-found branch body lost: %+v", p.Nodes[notFound.Next])
	}
	// Callers' Scope is not widened by the auto-declared symbol.
	if _, ok := opts.Scope["err_notFound"]; ok {
		t.Fatal("caller scope mutated")
	}

	p, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Create"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Nodes[p.Nodes[p.Entry].Next].Expression; got != "(err && !(err_alreadyExists))" {
		t.Fatalf("negated predicate not lowered: %s", got)
	}

	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Probe"), opts)
	d, ok := AsDiagnostic(err)
	if !ok || d.Construct != "error" || d.Source != "ctx.Err()" {
		t.Fatalf("predicate on an unsummarized error accepted: %v", err)
	}
}

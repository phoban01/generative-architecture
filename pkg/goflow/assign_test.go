package goflow

import (
	"github.com/phoban01/generative-architecture/pkg/expr"
	"go/ast"
	"reflect"
	"testing"
)

// fetchOptions summarizes Reconciler.get as a "get" node; db is bound to the
// model symbol found through Targets and err is discovered by type.
func fetchOptions() Options {
	opts := Options{
		Scope:   map[string]expr.Symbol{"found": {Type: "bool", Quint: "found"}},
		Targets: map[string]string{"db": "found"},
		Returns: []Return{{Type: "bool"}, {Error: true}},
	}
	opts.Call = func(c Callee, call *ast.CallExpr, convert Convert) (Node, bool, error) {
		switch c {
		case Callee{Package: "github.com/phoban01/generative-architecture/pkg/goflow/testdata/typed", Receiver: "Reconciler", Method: "get"}:
			return Node{Kind: "get"}, true, nil
		case Callee{Package: "github.com/phoban01/generative-architecture/pkg/goflow/testdata/typed", Receiver: "Reconciler", Method: "record"}:
			return Node{Kind: "record"}, true, nil
		}
		return Node{}, false, nil
	}
	return opts
}

func TestMultiValueAssignmentBindsResults(t *testing.T) {
	l := loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Fetch")
	p, err := ExtractTyped(l, fetchOptions())
	if err != nil {
		t.Fatal(err)
	}
	get := p.Nodes[p.Entry]
	if get.Kind != "get" || !reflect.DeepEqual(get.Results, []expr.Expression{"found", "err"}) {
		t.Fatalf("results not bound: %+v", get)
	}
	branch := p.Nodes[get.Next]
	if branch.Kind != "branch" || branch.Expression != "err" {
		t.Fatalf("error from the call not recognised: %+v", branch)
	}
	failed := p.Nodes[branch.Next]
	if failed.Kind != "return" || failed.ReturnError != "err" {
		t.Fatalf("returned error not bound: %+v", failed)
	}
	if p.Nodes[branch.Else].Kind != "record" {
		t.Fatalf("summarized call after the branch missing: %+v", p.Nodes[branch.Else])
	}

	blank := loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Exists")
	p, err = ExtractTyped(blank, fetchOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Nodes[p.Entry].Results; !reflect.DeepEqual(got, []expr.Expression{"", "err"}) {
		t.Fatalf("blank identifier not recorded: %+v", got)
	}

	// A left-hand name without a model symbol is a located diagnostic.
	opts := fetchOptions()
	opts.Targets = nil
	_, err = ExtractTyped(l, opts)
	d, ok := AsDiagnostic(err)
	if !ok || d.Construct != "assign" || d.Source != "db" {
		t.Fatalf("expected unknown target diagnostic, got %v", err)
	}
	// Auto-declared error symbols stay inside the extraction.
	if opts.Errors != nil || len(opts.Scope) != 1 {
		t.Fatalf("caller options mutated: %+v", opts)
	}
}

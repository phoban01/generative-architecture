package goflow

import (
	"go/ast"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

const kubeFixtures = "./pkg/goflow/testdata/kube"

// helperOptions summarizes the fixture's read method as a get.
func helperOptions() Options {
	opts := Options{Scope: map[string]expr.Symbol{}, Returns: []Return{{Zero: true}, {Error: true}}}
	opts.Call = func(c Callee, call *ast.CallExpr, convert Convert) (Node, bool, error) {
		if c.Package == "github.com/phoban01/generative-architecture/pkg/goflow/testdata/kube" && c.Receiver == "Reconciler" && c.Method == "read" {
			return Node{Kind: "get", Effect: "read"}, true, nil
		}
		return Node{}, false, nil
	}
	return opts
}

// kinds lists the node kinds of p in sorted order; nodes are built last
// first, so their order is not the source order.
func kinds(p *Program) string {
	var out []string
	for _, n := range p.Nodes {
		out = append(out, n.Kind)
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

// ctrl.SetControllerReference is a package variable of controller-runtime
// that holds controllerutil.SetControllerReference. It resolves to the
// same built-in summary.
func TestAliasResolvesToSummary(t *testing.T) {
	fields := map[string]expr.Symbol{
		"secret.metadata.name":                 {Type: "string", Quint: "secret_name"},
		"cm.metadata.name":                     {Type: "string", Quint: "cm_name"},
		"secret.metadata.ownerReferences.name": {Type: "string", Quint: "secret_owner"},
	}
	p, err := ExtractTyped(loadFixture(t, kubeFixtures, "Reconciler", "OwnAlias"), ownerOptions(fields))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range p.Nodes {
		if n.Kind == "assign" && strings.Contains(n.Source, "ctrl.SetControllerReference") {
			if n.Updates["secret_owner"] != "cm_name" {
				t.Fatalf("alias not summarized as the owner reference: %+v", n)
			}
			return
		}
	}
	t.Fatalf("no owner reference node in %+v", p.Nodes)
}

// client.IgnoreNotFound(err) lowers to err && !err_notFound, in a return
// and in a condition. A filter on an unsummarized error is a diagnostic.
func TestIgnoreNotFoundFilter(t *testing.T) {
	const filtered = "err && !err_notFound"
	p, err := ExtractTyped(loadFixture(t, kubeFixtures, "Reconciler", "IgnoreMissing"), helperOptions())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, n := range p.Nodes {
		if n.Kind == "return" && n.ReturnError == filtered {
			found = true
		}
	}
	if !found || kinds(p) != "branch,get,return,return" {
		t.Fatalf("filtered return missing in %+v", p.Nodes)
	}

	p, err = ExtractTyped(loadFixture(t, kubeFixtures, "Reconciler", "IgnoreMissingTest"), helperOptions())
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, n := range p.Nodes {
		if n.Kind == "branch" && n.Expression == filtered {
			found = true
		}
	}
	if !found {
		t.Fatalf("filtered condition missing in %+v", p.Nodes)
	}

	_, err = ExtractTyped(loadFixture(t, kubeFixtures, "Reconciler", "IgnoreParam"), helperOptions())
	d, ok := AsDiagnostic(err)
	if !ok || d.Construct != "error" || d.Line == 0 || d.Reason != "error filter on an unsummarized error" {
		t.Fatalf("filter on a parameter accepted: %v", err)
	}
}

// A statement that only calls a pure helper, such as sort.Slice on a local
// slice, adds no node. A call with a possible effect inside it is a
// diagnostic.
func TestPureHelperStatements(t *testing.T) {
	p, err := ExtractTyped(loadFixture(t, kubeFixtures, "Reconciler", "Tidy"), helperOptions())
	if err != nil {
		t.Fatal(err)
	}
	if kinds(p) != "return" {
		t.Fatalf("nodes %s, want only the return", kinds(p))
	}
	_, err = ExtractTyped(loadFixture(t, kubeFixtures, "Reconciler", "TidyCall"), helperOptions())
	d, ok := AsDiagnostic(err)
	if !ok || d.Construct != "call" || d.Line == 0 || !strings.Contains(d.Reason, "Reconciler.owner inside a pure helper call") {
		t.Fatalf("got %v, want a diagnostic on the call inside the comparator", err)
	}
}

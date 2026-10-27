package goflow

import (
	"go/ast"
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// copyOptions summarizes read and write by callee and maps the value entry
// of each object local to a model symbol.
func copyOptions() Options {
	opts := Options{
		Scope:   map[string]expr.Symbol{"sourceValue": {Type: "string", Quint: "sourceValue"}, "targetValue": {Type: "string", Quint: "targetValue"}},
		Returns: []Return{{Zero: true}, {Error: true}},
	}
	opts.Call = func(c Callee, call *ast.CallExpr, convert Convert) (Node, bool, error) {
		if c.Package != "github.com/phoban01/generative-architecture/pkg/goflow/testdata/typed" || c.Receiver != "Reconciler" {
			return Node{}, false, nil
		}
		switch c.Method {
		case "read":
			return Node{Kind: "get", Effect: "read"}, true, nil
		case "write":
			return Node{Kind: "update", Effect: "write"}, true, nil
		}
		return Node{}, false, nil
	}
	opts.Field = func(root string, path []string) (expr.Expression, bool) {
		if strings.Join(path, ".") != "data.value" {
			return "", false
		}
		switch root {
		case "source":
			return "sourceValue", true
		case "target":
			return "targetValue", true
		}
		return "", false
	}
	return opts
}

func TestObjectLocalsAndReturnedCalls(t *testing.T) {
	p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Copy"), copyOptions())
	if err != nil {
		t.Fatal(err)
	}
	// Declarations produce no nodes: the entry is the summarized read.
	read := p.Nodes[p.Entry]
	if read.Kind != "get" || read.Effect != "read" || read.Error != "err" || !reflect.DeepEqual(read.Results, []expr.Expression{"err"}) {
		t.Fatalf("read not summarized with its error bound: %+v", read)
	}
	branch := p.Nodes[read.Next]
	if branch.Kind != "branch" || branch.Expression != "err" {
		t.Fatalf("error branch missing: %+v", branch)
	}
	failed := p.Nodes[branch.Next]
	if failed.Kind != "return" || !reflect.DeepEqual(failed.Results, []expr.Expression{"", "err"}) {
		t.Fatalf("zero result not recorded as unmodelled: %+v", failed)
	}
	assign := p.Nodes[branch.Else]
	if assign.Kind != "assign" || !reflect.DeepEqual(assign.Updates, map[string]expr.Expression{"targetValue": "sourceValue"}) {
		t.Fatalf("map literal assignment not lowered through field paths: %+v", assign)
	}
	write := p.Nodes[assign.Next]
	if write.Kind != "update" || write.Error != ReturnedError || !reflect.DeepEqual(write.Results, []expr.Expression{ReturnedError}) || write.Source != "r.write(ctx, &target)" {
		t.Fatalf("returned call not summarized: %+v", write)
	}
	final := p.Nodes[write.Next]
	if final.Kind != "return" || final.ReturnError != ReturnedError || final.Source != "return Result{}, r.write(ctx, &target)" {
		t.Fatalf("return of the call's error missing: %+v", final)
	}
	if len(p.Nodes) != 6 {
		t.Fatalf("unexpected nodes %+v", p.Nodes)
	}

	// A non-zero result where Zero is declared, and a returned call nothing
	// summarizes, are located diagnostics.
	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Requeue"), copyOptions())
	if d, ok := AsDiagnostic(err); !ok || d.Construct != "return" || d.Line == 0 {
		t.Fatalf("non-zero result accepted: %v", err)
	}
	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Unsummarized"), copyOptions())
	if d, ok := AsDiagnostic(err); !ok || d.Construct != "call" || !strings.Contains(d.Reason, "Reconciler.after") {
		t.Fatalf("unsummarized returned call accepted: %v", err)
	}
	// A field the callback does not map is a diagnostic, not a dropped write.
	opts := copyOptions()
	opts.Field = func(root string, path []string) (expr.Expression, bool) {
		if root == "source" {
			return "sourceValue", true
		}
		return "", false
	}
	_, err = ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Copy"), opts)
	if d, ok := AsDiagnostic(err); !ok || d.Construct != "field" || d.Reason != "unmapped field target.data.value" {
		t.Fatalf("unmapped field assignment accepted: %v", err)
	}
}

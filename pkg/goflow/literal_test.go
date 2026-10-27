package goflow

import (
	"go/ast"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// A slice or struct literal assigned to a field is an opaque write when
// the abstraction selects nothing at or below the field. A selected field
// below it keeps the unmapped-field diagnostic.
func TestOpaqueLiteralWrites(t *testing.T) {
	options := func(selected map[string]string, mapKeys map[string][]string, withMapKeys bool) Options {
		opts := Options{Scope: map[string]expr.Symbol{}, Returns: []Return{{Zero: true}, {Error: true}}}
		for _, symbol := range selected {
			opts.Scope[symbol] = expr.Symbol{Type: "string", Quint: symbol}
		}
		opts.Call = func(c Callee, _ *ast.CallExpr, _ Convert) (Node, bool, error) {
			if c.Method == "create" {
				return Node{Kind: "create", Effect: "create"}, true, nil
			}
			return Node{}, false, nil
		}
		opts.Field = func(root string, path []string) (expr.Expression, bool) {
			symbol, ok := selected[root+"."+strings.Join(path, ".")]
			return expr.Expression(symbol), ok
		}
		if withMapKeys {
			opts.MapKeys = func(root string, path []string) []string {
				return mapKeys[root+"."+strings.Join(path, ".")]
			}
		}
		return opts
	}
	literals := loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Literals")
	for _, tc := range []struct {
		name        string
		method      string
		selected    map[string]string
		mapKeys     map[string][]string
		withMapKeys bool
		unmodelled  []string
		diagnostic  string
	}{
		{name: "nothing selected", withMapKeys: true, unmodelled: []string{"job.spec.containers", "job.spec.template"}},
		{name: "unrelated field selected", selected: map[string]string{"job.status": "s"}, withMapKeys: true, unmodelled: []string{"job.spec.containers", "job.spec.template"}},
		{name: "list entry field selected", selected: map[string]string{"job.spec.containers.image": "image"}, withMapKeys: true, diagnostic: "unmapped field job.spec.containers"},
		{name: "struct field selected", selected: map[string]string{"job.spec.template.image": "image"}, withMapKeys: true, diagnostic: "unmapped field job.spec.template"},
		{name: "type that contains itself", method: "RecursiveLiteral", withMapKeys: true, diagnostic: "unmapped field job.spec.tree"},
		{name: "map key selected", mapKeys: map[string][]string{"job.spec.containers.env": {"HOME"}}, withMapKeys: true, diagnostic: "unmapped field job.spec.containers"},
		{name: "map keys unknown", diagnostic: "unmapped field job.spec.containers"},
		{name: "call inside literal", method: "CallInLiteral", withMapKeys: true, diagnostic: "call to github.com/phoban01/generative-architecture/pkg/goflow/testdata/typed.Reconciler.image inside a literal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := literals
			if tc.method != "" {
				l = loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", tc.method)
			}
			p, err := ExtractTyped(l, options(tc.selected, tc.mapKeys, tc.withMapKeys))
			if tc.diagnostic != "" {
				d, ok := AsDiagnostic(err)
				if !ok || d.Line == 0 || !strings.Contains(d.Reason, tc.diagnostic) {
					t.Fatalf("got %v, want a located diagnostic containing %q", err, tc.diagnostic)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, n := range p.Nodes {
				if n.Kind == "assign" {
					if len(n.Updates) != 0 {
						t.Errorf("opaque write updates model state: %+v", n)
					}
					got = append(got, n.Unmodelled...)
				}
			}
			slices.Sort(got)
			if !reflect.DeepEqual(got, tc.unmodelled) {
				t.Fatalf("unmodelled writes %v, want %v", got, tc.unmodelled)
			}
		})
	}
}

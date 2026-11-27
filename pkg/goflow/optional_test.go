package goflow

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// A field of pointer-to-basic type is an optional value: *p is the value
// and p != nil tests it against the zero value of its type.
func TestOptionalFieldsLowerToZeroTests(t *testing.T) {
	l := loadFixture(t, "./pkg/goflow/testdata/typed", "Reconciler", "Paused")
	opts := Options{Scope: map[string]expr.Symbol{"suspend": {Type: "bool", Quint: "suspend"}, "deadline": {Type: "int", Quint: "deadline"}}, Returns: []Return{{Type: "bool"}}}
	opts.Field = func(root string, path []string) (expr.Expression, bool) {
		if root != "db" {
			return "", false
		}
		switch strings.Join(path, ".") {
		case "spec.suspend":
			return "suspend", true
		case "spec.deadline":
			return "deadline", true
		}
		return "", false
	}
	p, err := ExtractTyped(l, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"((suspend != false) && suspend)": false, "(deadline == 0)": false, "(deadline > 3)": false}
	for _, n := range p.Nodes {
		if _, ok := want[string(n.Expression)]; ok {
			want[string(n.Expression)] = true
		}
	}
	for e, seen := range want {
		if !seen {
			t.Errorf("no node with expression %s:\n%+v", e, p.Nodes)
		}
	}
}

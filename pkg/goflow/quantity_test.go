package goflow

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// Quantities built from constants are opaque constant values: a selected
// field takes the canonical string, an unselected one is a write without
// a value.
func TestQuantityWrites(t *testing.T) {
	cpu := map[string]expr.Symbol{"quota.spec.hard.cpu": {Type: "string", Quint: "quota_cpu"}}
	for _, tc := range []struct {
		name        string
		method      string
		fields      map[string]expr.Symbol
		withMapKeys bool
		mapKeys     []string
		updates     map[string]expr.Expression
		unmodelled  []string
		diagnostic  string
	}{
		{
			name: "nothing selected", method: "Quota", withMapKeys: true,
			unmodelled: []string{"quota.spec.hard.cpu", "quota.spec.hard.pods", "quota.spec.hard[requests.memory]"},
		},
		{
			name: "cpu selected", method: "Quota", fields: cpu, withMapKeys: true, mapKeys: []string{"cpu"},
			updates:    map[string]expr.Expression{"quota_cpu": `"1"`},
			unmodelled: []string{"quota.spec.hard.pods", "quota.spec.hard[requests.memory]"},
		},
		{
			name: "pods selected as int", method: "Quota", fields: map[string]expr.Symbol{"quota.spec.hard.pods": {Type: "int", Quint: "quota_pods"}}, withMapKeys: true,
			diagnostic: "must map to a string symbol",
		},
		{
			name: "dotted key selected", method: "Quota", withMapKeys: true, mapKeys: []string{"requests.memory"},
			diagnostic: `map literal key "requests.memory" cannot be a model path element`,
		},
		{
			name: "map keys unknown", method: "Quota",
			diagnostic: `map literal key "requests.memory" cannot be a model path element`,
		},
		{
			name: "not a constant", method: "QuotaFrom", withMapKeys: true,
			diagnostic: "resource.MustParse needs a constant string",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := ownerOptions(tc.fields)
			if tc.withMapKeys {
				opts.MapKeys = func(string, []string) []string { return tc.mapKeys }
			}
			p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/kube", "Reconciler", tc.method), opts)
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
			for _, n := range p.Nodes {
				if n.Kind != "assign" {
					continue
				}
				slices.Sort(n.Unmodelled)
				if len(n.Updates) == 0 {
					n.Updates = nil
				}
				if !reflect.DeepEqual(n.Updates, tc.updates) || !reflect.DeepEqual(n.Unmodelled, tc.unmodelled) {
					t.Fatalf("updates %v unmodelled %v, want %v and %v", n.Updates, n.Unmodelled, tc.updates, tc.unmodelled)
				}
				return
			}
			t.Fatalf("no assignment in %+v", p.Nodes)
		})
	}
}

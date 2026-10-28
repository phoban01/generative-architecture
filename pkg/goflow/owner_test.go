package goflow

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
)

// ownerOptions maps the listed field paths, root.json.path, to symbols of
// the given types.
func ownerOptions(fields map[string]expr.Symbol) Options {
	opts := Options{Scope: map[string]expr.Symbol{}, Returns: []Return{{Zero: true}, {Error: true}}}
	for _, s := range fields {
		opts.Scope[s.Quint] = s
	}
	opts.Field = func(root string, path []string) (expr.Expression, bool) {
		s, ok := fields[root+"."+strings.Join(path, ".")]
		return expr.Expression(s.Quint), ok
	}
	return opts
}

// controllerutil.SetControllerReference and SetOwnerReference write the
// owner reference of the local object through built-in summaries.
func TestOwnerReferenceSummaries(t *testing.T) {
	name := map[string]expr.Symbol{
		"secret.metadata.name": {Type: "string", Quint: "secret_name"},
		"cm.metadata.name":     {Type: "string", Quint: "cm_name"},
	}
	with := func(extra map[string]expr.Symbol) map[string]expr.Symbol {
		out := map[string]expr.Symbol{}
		for k, v := range name {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	owner := map[string]expr.Symbol{
		"secret.metadata.ownerReferences.name":       {Type: "string", Quint: "secret_owner"},
		"secret.metadata.ownerReferences.controller": {Type: "bool", Quint: "secret_controller"},
	}
	for _, tc := range []struct {
		name       string
		method     string
		fields     map[string]expr.Symbol
		updates    map[string]expr.Expression
		unmodelled []string
		diagnostic string
	}{
		{
			name: "controller reference selected", method: "Own", fields: with(owner),
			updates:    map[string]expr.Expression{"secret_owner": "cm_name", "secret_controller": "true", "err": "false"},
			unmodelled: []string{"secret.metadata.ownerReferences.blockOwnerDeletion", "secret.metadata.ownerReferences.uid"},
		},
		{
			name: "nothing selected", method: "Own", fields: name,
			updates:    map[string]expr.Expression{"err": "false"},
			unmodelled: []string{"secret.metadata.ownerReferences.blockOwnerDeletion", "secret.metadata.ownerReferences.controller", "secret.metadata.ownerReferences.name", "secret.metadata.ownerReferences.uid"},
		},
		{
			name: "owner reference returned", method: "Refer", fields: owner,
			diagnostic: "unmapped field cm.metadata.name, which the write of secret.metadata.ownerReferences.name reads",
		},
		{
			name: "owner reference returned with owner name", method: "Refer", fields: with(owner),
			updates:    map[string]expr.Expression{"secret_owner": "cm_name", "secret_controller": "false", "err": "false"},
			unmodelled: []string{"secret.metadata.ownerReferences.blockOwnerDeletion", "secret.metadata.ownerReferences.uid"},
		},
		{
			name: "kind selected", method: "Own", fields: with(map[string]expr.Symbol{"secret.metadata.ownerReferences.kind": {Type: "string", Quint: "secret_kind"}}),
			diagnostic: "field secret.metadata.ownerReferences.kind is selected",
		},
		{
			name: "option", method: "Unblocked", fields: name,
			diagnostic: "options are not summarized",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ExtractTyped(loadFixture(t, "./pkg/goflow/testdata/kube", "Reconciler", tc.method), ownerOptions(tc.fields))
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
			var found *Node
			for i, n := range p.Nodes {
				if n.Kind == "assign" && strings.Contains(n.Source, "Reference") {
					found = &p.Nodes[i]
				}
			}
			if found == nil {
				t.Fatalf("no owner reference node in %+v", p.Nodes)
			}
			slices.Sort(found.Unmodelled)
			if !reflect.DeepEqual(found.Updates, tc.updates) || !reflect.DeepEqual(found.Unmodelled, tc.unmodelled) {
				t.Fatalf("updates %v unmodelled %v, want %v and %v", found.Updates, found.Unmodelled, tc.updates, tc.unmodelled)
			}
		})
	}
}

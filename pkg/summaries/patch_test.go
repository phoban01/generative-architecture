package summaries

import (
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

func TestBuiltinDelete(t *testing.T) {
	s, err := Builtin().Lookup(ir.OpDelete, ir.ConsistencyDirect)
	if err != nil {
		t.Fatal(err)
	}
	if !equalNames(outcomeNames(s), "ok", "notFound", "error") {
		t.Fatalf("outcomes %v", outcomeNames(s))
	}
	ok, notFound := s.Outcomes[0], s.Outcomes[1]
	if ok.Guard != "$exists" || ok.Updates["$exists"] != "false" || len(ok.Updates) != 1 || ok.Error != "" {
		t.Fatalf("ok %+v", ok)
	}
	if notFound.Guard != "!$exists" || notFound.Error != ErrorNotFound || s.Outcomes[2].Error != ErrorTransient {
		t.Fatalf("notFound %+v error %+v", notFound, s.Outcomes[2])
	}
	guardsParse(t, s, map[string]any{"exists": true})
}

func TestBuiltinPatch(t *testing.T) {
	s, err := Builtin().Lookup(ir.OpPatch, ir.ConsistencyDirect)
	if err != nil {
		t.Fatal(err)
	}
	if !equalNames(outcomeNames(s), "ok", "conflict", "notFound", "error") {
		t.Fatalf("outcomes %v", outcomeNames(s))
	}
	ok, conflict := s.Outcomes[0], s.Outcomes[1]
	if ok.Updates["$object"] != "$value" || ok.Updates["$resourceVersion"] != "$resourceVersion + 1" || conflict.Error != ErrorConflict {
		t.Fatalf("ok %+v conflict %+v", ok, conflict)
	}
	b := bareBindings(s)
	base := map[string]any{"exists": true, "object": "", "value": "patched", "resourceVersion": int64(5), "snapshotVersion": int64(2)}
	cases := []struct {
		name    string
		options map[string]any
		ok      bool
	}{
		{"no precondition ignores a stale snapshot", map[string]any{}, true},
		{"matching precondition", map[string]any{"resourceVersion": int64(5)}, true},
		{"stale precondition", map[string]any{"resourceVersion": int64(2)}, false},
	}
	for _, c := range cases {
		vars := map[string]any{"options": c.options}
		for k, v := range base {
			vars[k] = v
		}
		guardsParse(t, s, vars)
		if evalTemplate(t, ok.Guard, b, vars) != c.ok || evalTemplate(t, conflict.Guard, b, vars) == c.ok {
			t.Fatalf("%s: ok=%v expected", c.name, c.ok)
		}
	}
}

func TestBuiltinApply(t *testing.T) {
	s, err := Builtin().Lookup(ir.OpApply, ir.ConsistencyDirect)
	if err != nil {
		t.Fatal(err)
	}
	if !equalNames(outcomeNames(s), "ok", "conflict", "error") {
		t.Fatalf("outcomes %v", outcomeNames(s))
	}
	ok, conflict := s.Outcomes[0], s.Outcomes[1]
	if ok.Updates["$exists"] != "true" || ok.Updates["$object"] != "$value" || ok.Updates["$owners[$options.fieldManager]"] != "$value" {
		t.Fatalf("ok %+v", ok)
	}
	// The ownership target substitutes to an index into the bound owners map.
	inst, err := ok.Instantiate(map[string]string{"$exists": "e", "$object": "o", "$resourceVersion": "rv", "$value": "v", "$snapshotVersion": "sv", "$options": "opts", "$owners": "owners", "$contested": "c"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Updates["owners[opts.fieldManager]"] != "v" {
		t.Fatalf("instantiated %+v", inst.Updates)
	}
	b := bareBindings(s)
	cases := []struct {
		name              string
		exists, contested bool
		options           map[string]any
		ok                bool
	}{
		{"creates when absent", false, false, map[string]any{"fieldManager": "m"}, true},
		{"applies unowned fields", true, false, map[string]any{"fieldManager": "m"}, true},
		{"conflicts on another manager's field", true, true, map[string]any{"fieldManager": "m"}, false},
		{"force takes the field", true, true, map[string]any{"fieldManager": "m", "force": true}, true},
		{"stale precondition", true, false, map[string]any{"fieldManager": "m", "resourceVersion": int64(1)}, false},
	}
	for _, c := range cases {
		vars := map[string]any{"exists": c.exists, "contested": c.contested, "options": c.options, "object": "", "value": "applied", "resourceVersion": int64(5), "snapshotVersion": int64(5), "owners": map[string]any{}}
		guardsParse(t, s, vars)
		if evalTemplate(t, ok.Guard, b, vars) != c.ok || evalTemplate(t, conflict.Guard, b, vars) == c.ok {
			t.Fatalf("%s: ok=%v expected", c.name, c.ok)
		}
	}
}

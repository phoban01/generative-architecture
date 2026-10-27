package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"generative-architecture/pkg/ir"
)

// diffDocuments writes two IR documents that differ in the field path of
// one watch.
func diffDocuments(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	prov := ir.Provenance{Generator: "test", Source: ir.Source{File: "f.go", Line: 1, SHA256: "s"}}
	doc := ir.Document{
		Schema:      ir.SchemaVersion,
		Controllers: []ir.Controller{{ID: "c", Name: "C", Provenance: prov}},
		Watches:     []ir.Watch{{ID: "w", Controller: "c", Resource: ir.GVK{Version: "v1", Kind: "ConfigMap"}, Mapping: ir.KeyMapping{Kind: ir.MappingField, FieldPath: []string{"Spec", "Ref"}}, Evidence: ir.Extracted, Provenance: prov}},
	}
	old := filepath.Join(dir, "old.yaml")
	if err := doc.Write(old, "sample"); err != nil {
		t.Fatal(err)
	}
	doc.Watches[0].Mapping.FieldPath = []string{"Spec", "Other"}
	changed := filepath.Join(dir, "new.yaml")
	if err := doc.Write(changed, "sample"); err != nil {
		t.Fatal(err)
	}
	return old, changed
}

func TestDiffCommand(t *testing.T) {
	old, changed := diffDocuments(t)
	var out, errOut bytes.Buffer
	err := run([]string{"diff", old, changed}, &out, &errOut)
	var exit exitError
	if !errors.As(err, &exit) || exit.code != 2 {
		t.Fatalf("expected exit status 2, got %v", err)
	}
	if got := out.String(); got != "~ watch w (mapping)\n" {
		t.Fatalf("unexpected output %q", got)
	}

	out.Reset()
	if err := run([]string{"diff", "--json", old, changed}, &out, &errOut); !errors.Is(err, errDiffers) {
		t.Fatalf("expected errDiffers, got %v", err)
	}
	var d ir.Delta
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Added) != 0 || len(d.Removed) != 0 || len(d.Changed) != 1 || d.Changed[0].Kind != ir.RecordWatch {
		t.Fatalf("unexpected JSON delta %+v", d)
	}

	out.Reset()
	if err := run([]string{"diff", old, old}, &out, &errOut); err != nil || out.Len() != 0 {
		t.Fatalf("identical documents: err %v, output %q", err, out.String())
	}
	if err := run([]string{"diff", old}, &out, &errOut); err == nil {
		t.Fatal("expected a usage error for one argument")
	}
}

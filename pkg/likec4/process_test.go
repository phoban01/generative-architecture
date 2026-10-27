package likec4

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

func shortProcess() ir.Process {
	src := func(line int) ir.Source { return ir.Source{File: "controller.go", Line: line} }
	return ir.Process{ID: "db", Controller: "Reconciler", Method: "Reconcile", Entry: "n0", Nodes: []ir.Node{
		{ID: "n0", Kind: ir.NodeEffect, Effect: "readDatabase", Next: "n1", Source: src(10), Code: "found := store.DatabaseExists()"},
		{ID: "n1", Kind: ir.NodeBranch, Expression: "!found", Next: "n2", Else: "n3", Source: src(11), Code: "!found"},
		{ID: "n2", Kind: ir.NodeReturn, Source: src(12)},
		{ID: "n3", Kind: ir.NodeEffect, Effect: "publish", Next: "n2", Source: src(14)},
	}}
}

func TestProcessViewGolden(t *testing.T) {
	elements, relations, view, err := ProcessView("db_flow", "Reconcile flow", shortProcess())
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 4 || len(relations) != 4 {
		t.Fatalf("%d elements, %d relations", len(elements), len(relations))
	}
	var w Workspace
	if err := w.Add(elements, relations, view); err != nil {
		t.Fatal(err)
	}
	golden(t, "process", w)
	likec4Validate(t, w)
}

func TestProcessViewRejectsDanglingEdges(t *testing.T) {
	p := shortProcess()
	p.Nodes[1].Else = "n9"
	if _, _, _, err := ProcessView("v", "t", p); err == nil {
		t.Fatal("branch to an unknown node accepted")
	}
	p = shortProcess()
	p.Entry = "n9"
	if _, _, _, err := ProcessView("v", "t", p); err == nil {
		t.Fatal("unknown entry accepted")
	}
}

// Source renders the same workspace as one file, which LikeC4 also parses.
func TestSourceIsOneFile(t *testing.T) {
	src, err := twoElements().Source()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(src, []byte("DO NOT EDIT")) != 1 || !bytes.Contains(src, []byte("specification {")) || !bytes.Contains(src, []byte("view overview")) {
		t.Fatalf("single file:\n%s", src)
	}
	bin := os.Getenv("LIKEC4_BIN")
	if bin == "" {
		t.Skip("LIKEC4_BIN not set")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "all.c4"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "validate", dir).CombinedOutput()
	if err != nil || strings.Contains(strings.ToLower(string(out)), "error") {
		t.Fatalf("likec4 validate: %v\n%s", err, out)
	}
}

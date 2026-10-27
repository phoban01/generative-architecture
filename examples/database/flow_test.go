package database

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

// The example's diagram is one pkg/likec4 workspace: every extracted
// process node appears as a step with its Go source and line, every
// extracted watch and dynamic-client effect appears in the resource view
// with its source, the derived effect is declared, generated/architecture.c4
// is current and is the only .c4 file, and LikeC4 parses the generated
// directory when LIKEC4_BIN is set.
func TestLikeC4DelegatesToWorkspaceViews(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	diagram, err := e.LikeC4()
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Process()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != len(e.Program.Nodes) {
		t.Fatalf("%d IR nodes for %d extracted nodes", len(p.Nodes), len(e.Program.Nodes))
	}
	for _, n := range p.Nodes {
		if !strings.Contains(diagram, "db_"+n.ID+" = step '"+strings.ReplaceAll(n.Code, "'", `\'`)+"'") || !strings.Contains(diagram, "source: controller.go:") {
			t.Fatalf("step %s (%q) missing from the diagram:\n%s", n.ID, n.Code, diagram)
		}
	}
	if strings.Count(diagram, "specification {") != 1 {
		t.Fatal("the diagram has more than one specification")
	}
	w, err := e.Workspace()
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]ir.Evidence{}
	for _, r := range w.Relations {
		labels[r.Label+" "+strings.SplitN(r.Description, ":", 2)[0]] = r.Evidence
		if r.Label != "next" && r.Label != "true" && r.Label != "false" && r.Source.File == "" {
			t.Errorf("relation %s -> %s %s has no source", r.From, r.To, r.Label)
		}
	}
	for label, want := range map[string]ir.Evidence{
		"writes createInstance":        ir.Extracted,
		"reads readDatabase":           ir.Extracted,
		"reads readInstance":           ir.Extracted,
		"writes publish":               ir.Extracted,
		"uses schema of checkContract": ir.Declared,
	} {
		if got, ok := labels[label]; !ok || got != want {
			t.Errorf("relation %q: evidence %q, want %q", label, got, want)
		}
	}
	watches := 0
	for _, r := range w.Relations {
		if r.Label == "watches" {
			watches++
		}
	}
	if watches != len(e.Watches.Rules) {
		t.Errorf("%d watch relations for %d extracted watches", watches, len(e.Watches.Rules))
	}
	committed, err := os.ReadFile(filepath.Join("generated", "architecture.c4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != diagram {
		t.Fatal("generated/architecture.c4 is stale; run go run ./cmd/walkthrough")
	}
	others, err := filepath.Glob(filepath.Join("generated", "*.c4"))
	if err != nil || len(others) != 1 {
		t.Fatalf("generated .c4 files %v: %v", others, err)
	}
	bin := os.Getenv("LIKEC4_BIN")
	if bin == "" {
		t.Skip("LIKEC4_BIN not set; skipping LikeC4 validation")
	}
	out, err := exec.Command(bin, "validate", "generated").CombinedOutput()
	if err != nil || strings.Contains(strings.ToLower(string(out)), "error") {
		t.Fatalf("likec4 validate generated: %v\n%s", err, out)
	}
}

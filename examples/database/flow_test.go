package database

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FlowLikeC4 is likec4.ProcessView of the extracted process: every node
// appears as a step with its Go source and line, the committed
// generated/process.c4 is current, and LikeC4 parses the generated
// directory when LIKEC4_BIN is set.
func TestFlowLikeC4DelegatesToProcessView(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	flow, err := e.FlowLikeC4()
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
		if !strings.Contains(flow, "db_"+n.ID+" = step '"+strings.ReplaceAll(n.Code, "'", `\'`)+"'") || !strings.Contains(flow, "source: controller.go:") {
			t.Fatalf("step %s (%q) missing from the flow:\n%s", n.ID, n.Code, flow)
		}
	}
	committed, err := os.ReadFile(filepath.Join("generated", "process.c4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != flow {
		t.Fatal("generated/process.c4 is stale; run go run ./cmd/walkthrough")
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

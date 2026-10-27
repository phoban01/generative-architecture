package configmirror

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The committed diagram is the pkg/likec4 resource view of the generated
// model: one specification, one relation per model effect, and LikeC4
// parses it when LIKEC4_BIN is set.
func TestDiagramIsCurrent(t *testing.T) {
	spec, err := Generate(".")
	if err != nil {
		t.Fatal(err)
	}
	diagram, err := Diagram(spec)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(DiagramFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != diagram {
		t.Fatalf("%s is stale; run go run ./cmd/library-demo --generate-only", DiagramFile)
	}
	if strings.Count(diagram, "specification {") != 1 {
		t.Fatal("the diagram has more than one specification")
	}
	for _, e := range spec.Effects {
		if !strings.Contains(diagram, "'"+e.ID+": ") {
			t.Errorf("effect %s missing from the diagram", e.ID)
		}
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

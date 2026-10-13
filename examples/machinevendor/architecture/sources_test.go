package architecture

import (
	"generative-architecture/pkg/sourcegen"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcePortTypeChangeRejected(t *testing.T) {
	p := project(t)
	// Isolate the descriptor and its source inventory. No live project files change.
	root := t.TempDir()
	var d sourcegen.Descriptor
	if err := ReadResult(filepath.Join(p.Root, p.System.Sources.Descriptor), "SourceDescriptor", &d); err != nil {
		t.Fatal(err)
	}
	for path := range d.SourceFiles {
		b, err := os.ReadFile(filepath.Join(p.Root, path))
		if err != nil {
			t.Fatal(err)
		}
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Represents an extractor result after the Go boolean becomes a string.
	for i := range d.Controllers[0].Ports {
		if d.Controllers[0].Ports[i].Name == "eligible" {
			d.Controllers[0].Ports[i].Type = "map(string, string)"
		}
	}
	path := filepath.Join(root, p.System.Sources.Descriptor)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteResult(path, "SourceDescriptor", "sources", d); err != nil {
		t.Fatal(err)
	}
	p.Root = root
	_, err := p.Compile()
	if err == nil || !strings.Contains(err.Error(), "mac.eligible incompatible") {
		t.Fatalf("source/contract drift not rejected: %v", err)
	}
}

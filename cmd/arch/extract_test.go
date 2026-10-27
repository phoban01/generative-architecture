package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/report"
)

// extractTo runs arch extract on a project document into a temporary
// directory and returns the directory and the command's error.
func extractTo(t *testing.T, projectPath string) (string, error) {
	t.Helper()
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := run([]string{"--project", projectPath, "--out", out, "extract"}, &stdout, &stderr)
	return out, err
}

func readSupport(t *testing.T, out string) report.Support {
	t.Helper()
	s, err := report.Read(filepath.Join(out, supportFile))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExtractReferenceSystem(t *testing.T) {
	out, err := extractTo(t, "../../examples/refsystem/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ir.Read(filepath.Join(out, irFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Controllers) != 0 || len(doc.Watches) != 0 {
		t.Fatalf("reference system has API types only, got %+v", doc)
	}
	s := readSupport(t, out)
	if len(s.Included)+len(s.Summarized)+len(s.Unsupported) != 0 {
		t.Fatalf("expected an empty support report, got %+v", s)
	}
}

func TestExtractControllerWithoutSetup(t *testing.T) {
	out, err := extractTo(t, "../../examples/configmirror/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ir.Read(filepath.Join(out, irFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Controllers) != 1 || doc.Controllers[0].Receiver != "Reconciler" || len(doc.Watches) != 0 {
		t.Fatalf("expected one controller and no watches, got %+v", doc)
	}
	// The missing setup is reported, not silently dropped.
	s := readSupport(t, out)
	if len(s.Included) != 1 || len(s.Summarized) != 1 || s.Summarized[0].Kind != "setup" || s.Summarized[0].Location.File != "examples/configmirror/controller.go" || len(s.Unsupported) != 0 {
		t.Fatalf("unexpected support report %+v", s)
	}
}

func TestExtractWatches(t *testing.T) {
	out, err := extractTo(t, "testdata/watched/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ir.Read(filepath.Join(out, irFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Controllers) != 1 || len(doc.Watches) != 2 {
		t.Fatalf("expected one controller with two watches, got %+v", doc)
	}
	c := doc.Controllers[0]
	primary, owned := doc.Watches[0], doc.Watches[1]
	if primary.Controller != c.ID || !primary.Primary || primary.Resource != (ir.GVK{Version: "v1", Kind: "ConfigMap"}) || len(primary.Predicates) != 1 || primary.Predicates[0].Kind != "generationChanged" {
		t.Fatalf("primary watch %+v", primary)
	}
	if owned.Resource.Kind != "Secret" || owned.Mapping.Kind != ir.MappingOwner || owned.Mapping.OwnerGVK == nil || owned.Mapping.OwnerGVK.Kind != "ConfigMap" {
		t.Fatalf("owned watch %+v", owned)
	}
	if primary.Provenance.Source.File != "cmd/arch/testdata/watched/controller.go" || primary.Provenance.Source.Line == 0 {
		t.Fatalf("watch provenance %+v", primary.Provenance)
	}
	s := readSupport(t, out)
	if len(s.Included) != 3 || len(s.Summarized) != 0 || len(s.Unsupported) != 0 {
		t.Fatalf("unexpected support report %+v", s)
	}
	again, err := extractTo(t, "testdata/watched/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(out, irFile))
	b, _ := os.ReadFile(filepath.Join(again, irFile))
	if !bytes.Equal(a, b) {
		t.Fatal("extraction is not deterministic")
	}
}

func TestExtractFailsClosedOnUnknownResource(t *testing.T) {
	out, err := extractTo(t, "testdata/watched/partial.yaml")
	if !errors.Is(err, errUnsupported) {
		t.Fatalf("expected unsupported constructs, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, irFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no IR may be written when constructs are unsupported: %v", err)
	}
	s := readSupport(t, out)
	if len(s.Unsupported) != 1 {
		t.Fatalf("expected one unsupported construct, got %+v", s.Unsupported)
	}
	u := s.Unsupported[0]
	if u.Kind != "resource" || u.Location.File != "cmd/arch/testdata/watched/controller.go" || u.Location.Line != 27 {
		t.Fatalf("diagnostic not located at the Owns call: %+v", u)
	}
}

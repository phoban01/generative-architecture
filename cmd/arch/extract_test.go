package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/report"
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

// refsystemDir holds the reference system and its committed extraction.
const refsystemDir = "../../examples/refsystem"

// TestExtractReferenceSystem runs arch --strict extract on the reference
// system. It finds the three controllers and their seven watches and no
// unsupported construct. The reference system has no abstraction yet, so
// every field its Reconcile methods touch is unresolved and verification
// mode still fails on those, after it writes the IR. The committed IR and
// support report must match the run byte for byte; regenerate them with
// go run ./cmd/arch --project examples/refsystem/project.yaml --out examples/refsystem/generated extract.
func TestExtractReferenceSystem(t *testing.T) {
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := run([]string{"--project", filepath.Join(refsystemDir, "project.yaml"), "--out", out, "--strict", "extract"}, &stdout, &stderr)
	if !errors.Is(err, errStrict) || errors.Is(err, errUnsupported) {
		t.Fatalf("expected verification mode to fail on unresolved fields only, got %v", err)
	}
	doc, err := ir.Read(filepath.Join(out, irFile))
	if err != nil {
		t.Fatal(err)
	}
	var controllers []string
	for _, c := range doc.Controllers {
		controllers = append(controllers, c.Name)
	}
	if want := []string{"BackupReconciler", "DatabaseReconciler", "TenantReconciler"}; !slices.Equal(controllers, want) {
		t.Fatalf("controllers %q, want %q", controllers, want)
	}
	var watches []string
	for _, w := range doc.Watches {
		m := w.Mapping
		row := w.Controller[strings.LastIndex(w.Controller, ".")+1:] + " " + w.Resource.Kind + " " + m.Kind
		switch m.Kind {
		case ir.MappingOwner:
			row += " " + m.OwnerGVK.Kind
		case ir.MappingLabel:
			row += " " + m.Dependent.Kind + " " + m.Label
		case ir.MappingReference:
			row += " " + m.Dependent.Kind + " " + strings.Join(m.DependentPath, ".")
		}
		watches = append(watches, row)
	}
	want := []string{
		"BackupReconciler Backup self",
		"BackupReconciler Job owner Backup",
		"BackupReconciler Database reference Backup Spec.Database",
		"DatabaseReconciler Database self",
		"DatabaseReconciler Secret owner Database",
		"DatabaseReconciler Tenant label Database refsystem.example.org/tenant",
		"TenantReconciler Tenant self",
	}
	if !slices.Equal(watches, want) {
		t.Fatalf("watches\n%s\nwant\n%s", strings.Join(watches, "\n"), strings.Join(want, "\n"))
	}
	s := readSupport(t, out)
	if len(s.Unsupported) != 0 {
		t.Fatalf("unsupported %+v, want none", s.Unsupported)
	}
	var reconciles int
	for _, it := range s.Included {
		if it.Kind == "reconcile" {
			reconciles++
		}
	}
	if reconciles != 3 {
		t.Fatalf("%d Reconcile methods extracted, want 3", reconciles)
	}
	if len(s.Unresolved) == 0 {
		t.Fatal("no unresolved field, but the reference system has no abstraction")
	}
	for _, it := range s.Unresolved {
		if it.Kind != "field" || !strings.HasPrefix(it.Location.File, "examples/refsystem/controllers/") || it.Location.Line == 0 {
			t.Fatalf("unresolved item %+v is not a located controller field", it)
		}
	}
	for _, name := range []string{irFile, supportFile} {
		got, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		committed := filepath.Join(refsystemDir, "generated", name)
		want, err := os.ReadFile(committed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; regenerate it with go run ./cmd/arch --project examples/refsystem/project.yaml --out examples/refsystem/generated extract", committed)
		}
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
	// The missing setup is reported, not silently dropped. Reconcile
	// extracts, and without an abstraction both map fields it touches are
	// unresolved. With the abstraction of testdata/configmirror they
	// resolve.
	s := readSupport(t, out)
	if len(s.Included) != 2 || s.Included[1].Kind != "reconcile" || len(s.Summarized) != 1 || s.Summarized[0].Kind != "setup" || s.Summarized[0].Location.File != "examples/configmirror/controller.go" || len(s.Unsupported) != 0 {
		t.Fatalf("unexpected support report %+v", s)
	}
	if got := constructs(s.Unresolved); len(got) != 2 || !strings.HasPrefix(got[0], "source.data.value at ") || !strings.HasPrefix(got[1], "target.data.value at ") {
		t.Fatalf("unresolved %q", got)
	}
	out, err = extractTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	if s := readSupport(t, out); len(s.Unsupported)+len(s.Unresolved) != 0 {
		t.Fatalf("abstracted support report %+v", s)
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
	// The controller, its two watches and its Reconcile method.
	if len(s.Included) != 4 || len(s.Summarized) != 0 || len(s.Unsupported) != 0 || len(s.Unresolved) != 0 {
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

// TestExtractResolvesGroupVersion checks that a watched type resolves to
// the catalog entry of its package's group and version, not of its package
// name. The catalog lists Widget in two groups with the same version.
func TestExtractResolvesGroupVersion(t *testing.T) {
	for _, tc := range []struct {
		name, project string
		want          ir.GVK
		unsupported   *report.Item
	}{
		{"GroupVersion variable, package name is not the version", "testdata/grouped/widget.yaml",
			ir.GVK{Group: "widgets.example.test", Version: "v1beta1", Kind: "Widget"}, nil},
		{"group marker", "testdata/grouped/marked.yaml",
			ir.GVK{Group: "other.example.test", Version: "v1beta1", Kind: "Widget"}, nil},
		{"no GroupVersion and no marker", "testdata/grouped/gadget.yaml", ir.GVK{},
			&report.Item{Kind: "group-version", Location: report.Location{File: "cmd/arch/testdata/grouped/gadget/controller.go", Line: 25}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := extractTo(t, tc.project)
			if tc.unsupported != nil {
				if !errors.Is(err, errUnsupported) {
					t.Fatalf("expected unsupported constructs, got %v", err)
				}
				s := readSupport(t, out)
				if len(s.Unsupported) != 1 || s.Unsupported[0].Kind != tc.unsupported.Kind || s.Unsupported[0].Location != tc.unsupported.Location {
					t.Fatalf("unsupported %+v, want %+v", s.Unsupported, *tc.unsupported)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			doc, err := ir.Read(filepath.Join(out, irFile))
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Watches) != 1 || doc.Watches[0].Resource != tc.want {
				t.Fatalf("watches %+v, want one of %s", doc.Watches, tc.want)
			}
		})
	}
}

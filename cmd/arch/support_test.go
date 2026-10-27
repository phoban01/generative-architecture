package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// support runs arch support and returns its output and error.
func support(t *testing.T, projectPath string, strict bool) (string, string, error) {
	t.Helper()
	args := []string{"--project", projectPath, "--out", t.TempDir()}
	if strict {
		args = append(args, "--strict")
	}
	var stdout, stderr bytes.Buffer
	err := run(append(args, "support"), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func TestSupportReferenceSystemHasNoUnsupportedItems(t *testing.T) {
	out, _, err := support(t, "../../examples/refsystem/project.yaml", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, report.Unsupported) {
		t.Fatalf("unexpected unsupported item:\n%s", out)
	}
}

func TestSupportStrictFailsOnUnsupportedItem(t *testing.T) {
	out, stderr, err := support(t, "testdata/watched/partial.yaml", false)
	if err != nil {
		t.Fatalf("inventory mode must pass: %v", err)
	}
	var row string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, report.Unsupported) {
			row = line
		}
	}
	if !strings.Contains(row, "cmd/arch/testdata/watched/controller.go:27") || !strings.HasPrefix(out, "STATUS") {
		t.Fatalf("table lacks the located unsupported row:\n%s", out)
	}
	if !strings.Contains(stderr, "inventory mode: 1 ") {
		t.Fatalf("inventory mode does not report the item: %q", stderr)
	}
	if _, _, err := support(t, "testdata/watched/partial.yaml", true); !errors.Is(err, errStrict) {
		t.Fatalf("verification mode must fail, got %v", err)
	}
}

func TestExtractStrictPassesWhenComplete(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--project", "testdata/watched/project.yaml", "--out", t.TempDir(), "--strict", "extract"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateHonoursStrict(t *testing.T) {
	const projectPath = "../../examples/refsystem/project.yaml"
	out, err := extractTo(t, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	gen := func(strict bool) (string, error) {
		args := []string{"--project", projectPath, "--out", out}
		if strict {
			args = append(args, "--strict")
		}
		var stdout, stderr bytes.Buffer
		err := run(append(args, "generate"), &stdout, &stderr)
		return stderr.String(), err
	}
	writeReport := func(s report.Support) {
		t.Helper()
		if err := s.Write(filepath.Join(out, supportFile)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gen(true); err != nil {
		t.Fatalf("a complete report passes verification mode: %v", err)
	}
	// An unsupported construct outside the selected packages is out of
	// scope.
	writeReport(report.Support{Unsupported: []report.Item{{Kind: "statement", Construct: "go", Location: report.Location{File: "examples/refsystem/controllers/tenant/controller.go", Line: 3}}}})
	if _, err := gen(true); err != nil {
		t.Fatalf("out-of-scope item failed verification mode: %v", err)
	}
	writeReport(report.Support{Unsupported: []report.Item{{Kind: "statement", Construct: "go", Location: report.Location{File: "examples/refsystem/api/v1alpha1/types.go", Line: 3}}}})
	stderr, err := gen(false)
	if err != nil || !strings.Contains(stderr, "inventory mode") {
		t.Fatalf("inventory mode must pass and report: %v %q", err, stderr)
	}
	if _, err := gen(true); !errors.Is(err, errStrict) || !strings.Contains(err.Error(), "types.go:3") {
		t.Fatalf("verification mode must fail at the item, got %v", err)
	}
	if err := os.Remove(filepath.Join(out, supportFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := gen(true); !errors.Is(err, errStrict) {
		t.Fatalf("verification mode needs the support report, got %v", err)
	}
}

func TestUnresolvedFactsAreReported(t *testing.T) {
	doc := ir.Document{Watches: []ir.Watch{
		{ID: "w1", Evidence: ir.Extracted},
		{ID: "w2", Evidence: ir.Unresolved, Provenance: ir.Provenance{Source: ir.Source{File: "a/b.go", Line: 7}}},
	}}
	items := unresolvedFacts(doc)
	if len(items) != 1 || items[0].Construct != "w2" || location(items[0].Location) != "a/b.go:7" {
		t.Fatalf("unresolved facts %+v", items)
	}
}

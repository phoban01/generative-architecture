package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/project"
)

func runIn(t *testing.T, args ...string) error {
	t.Helper()
	var out, errOut bytes.Buffer
	return run(args, &out, &errOut)
}

func TestInitWritesProjectAndConstraints(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("go.mod", []byte("module example.org/sample\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runIn(t, "init", "--module", "example.org/sample", "--package", "./...", "--name", "sample"); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "sample" || p.Spec.Module != "example.org/sample" || strings.Join(p.Spec.Packages, ",") != "./..." || strings.Join(p.Spec.Constraints, ",") != constraintsFile {
		t.Fatalf("unexpected project %s %+v", p.Name, p.Spec)
	}
	var constraints struct {
		Properties []map[string]string `yaml:"properties"`
	}
	if _, err := krm.Read(constraintsFile, constraintsKind, &constraints); err != nil {
		t.Fatal(err)
	}
	if len(constraints.Properties) != 0 {
		t.Fatalf("constraints must start empty, got %+v", constraints)
	}

	// A second run refuses to overwrite and leaves both files untouched.
	if err := os.WriteFile(constraintsFile, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = runIn(t, "init", "--module", "example.org/other")
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), projectFile) || !strings.Contains(err.Error(), constraintsFile) {
		t.Fatalf("expected an overwrite refusal naming both files, got %v", err)
	}
	if b, _ := os.ReadFile(constraintsFile); string(b) != "edited\n" {
		t.Fatalf("refused init changed %s", constraintsFile)
	}

	// --force replaces both files.
	if err := runIn(t, "init", "--module", "example.org/sample", "--force"); err != nil {
		t.Fatal(err)
	}
	if _, err := krm.Read(constraintsFile, constraintsKind, &constraints); err != nil {
		t.Fatal(err)
	}
}

func TestInitRejectsBadInput(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"init"},
		{"init", "--module", "example.org/x", "--package", "pkg/x"},
		{"init", "--module", "example.org/x", "--name", "Not_A_Name"},
	} {
		if err := runIn(t, args...); err == nil {
			t.Fatalf("%v: expected an error", args)
		}
	}
	for _, f := range []string{projectFile, constraintsFile} {
		if _, err := os.Stat(f); err == nil {
			t.Fatalf("rejected init wrote %s", f)
		}
	}
}

func TestProjectName(t *testing.T) {
	for in, want := range map[string]string{"My_Repo": "my-repo", "a.b": "a-b", "__": "project"} {
		if got := projectName(in); got != want {
			t.Fatalf("projectName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Without --crd and --discovery, arch init writes an empty catalog section
// with a comment that names both fields, and the document still loads.
func TestInitWritesEmptyCatalogComment(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module example.org/sample\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runIn(t, "init", "--module", "example.org/sample", "--name", "sample"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# arch extract resolves watched resources through the catalog", "crds: []", "discovery: []", "--crd and\n  # --discovery"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("project document lacks %q:\n%s", want, b)
		}
	}
	p, err := project.Load(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Spec.Catalog.CRDs)+len(p.Spec.Catalog.Discovery) != 0 {
		t.Fatalf("catalog must be empty: %+v", p.Spec.Catalog)
	}
}

// --crd takes a directory or a file and --discovery a file; the written
// catalog loads the CronJob CRD and the built-in kinds without edits.
func TestInitWritesCatalog(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "external", "cronjob")
	crd, err := os.ReadFile(filepath.Join(src, "config", "crd", "bases", "batch.tutorial.kubebuilder.io_cronjobs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := os.ReadFile(filepath.Join(src, "catalog", "batch-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	for name, b := range map[string][]byte{"go.mod": []byte("module tutorial.kubebuilder.io/project\n\ngo 1.26.0\n"), "crd/bases/cronjobs.yaml": crd, "crd/bases/README.md": []byte("not a manifest\n"), "catalog/batch-v1.json": snapshot} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runIn(t, "init", "--module", "tutorial.kubebuilder.io/project", "--name", "cronjob", "--crd", "crd/bases", "--discovery", "catalog/batch-v1.json"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "# arch extract resolves") {
		t.Error("a filled catalog must carry no placeholder comment")
	}
	p, err := project.Load(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Spec.Catalog.CRDs, ",") != "crd/bases/cronjobs.yaml" || strings.Join(p.Spec.Catalog.Discovery, ",") != "catalog/batch-v1.json" {
		t.Fatalf("catalog %+v", p.Spec.Catalog)
	}
	c, err := p.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup(ir.GVK{Group: "batch.tutorial.kubebuilder.io", Version: "v1", Kind: "CronJob"}); !ok {
		t.Error("catalog does not resolve CronJob")
	}
	for _, args := range [][]string{
		{"--crd", "missing.yaml"},
		{"--crd", "catalog/batch-v1.json", "--discovery", "crd/bases"},
		{"--crd", "crd"}, // a directory with no manifest files
	} {
		err := runIn(t, append([]string{"init", "--module", "example.org/x", "--force"}, args...)...)
		if err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

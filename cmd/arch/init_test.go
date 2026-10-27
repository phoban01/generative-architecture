package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/project"
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

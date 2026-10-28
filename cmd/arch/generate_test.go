package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/goflow"
)

// configMirror is the ConfigMirror sample with its model declarations in
// the project document.
const configMirror = "testdata/configmirror/project.yaml"

// generateTo runs arch extract and arch generate into a temporary
// directory and returns the directory and generate's error.
func generateTo(t *testing.T, projectPath string) (string, error) {
	t.Helper()
	out, err := extractTo(t, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	return out, run([]string{"--project", projectPath, "--out", out, "generate"}, &stdout, &stderr)
}

func TestGenerateConfigMirror(t *testing.T) {
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	// The model equals the one examples/configmirror/generate.go builds by
	// hand from the same inputs.
	for file, want := range map[string]string{modelFile: "Model.qnt", specFile: "model.yaml"} {
		got, err := os.ReadFile(filepath.Join(out, file))
		if err != nil {
			t.Fatal(err)
		}
		golden, err := os.ReadFile(filepath.Join("../../examples/configmirror/generated", want))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, golden) {
			t.Errorf("%s differs from the hand-assembled %s", file, want)
		}
	}
	for _, view := range []string{"context", "process", "resources", "runtime"} {
		if _, err := os.Stat(filepath.Join(out, likec4Dir, "views", view+".c4")); err != nil {
			t.Errorf("view %s: %v", view, err)
		}
	}
	r, err := readReceipt(filepath.Join(out, receiptsFile))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, irFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Generated) != 1 || r.Generated[0].Path != irFile || r.Generated[0].SHA256 != fmt.Sprintf("%x", sha256.Sum256(b)) {
		t.Fatalf("receipt does not record ir.yaml: %+v", r.Generated)
	}
	recorded := map[string]bool{}
	for _, f := range r.Files {
		recorded[f.Path] = true
	}
	for _, want := range []string{"examples/configmirror/controller.go", "cmd/arch/testdata/configmirror/project.yaml", "cmd/arch/testdata/configmirror/abstraction.yaml", "cmd/arch/testdata/configmirror/constraints.yaml", "go.mod"} {
		if !recorded[want] {
			t.Errorf("receipt does not record %s", want)
		}
	}
	if !r.Owns("examples/configmirror/controller.go") || !r.Owns("cmd/arch/testdata/configmirror/project.yaml") || r.Owns("pkg/model/model.go") || r.Owns("go.mod") {
		t.Errorf("receipt owned entries %v", r.Owned)
	}
	if r.Bounds["maxInvocations"] != 3 || r.Tools["go"] == "" {
		t.Errorf("receipt bounds %v tools %v", r.Bounds, r.Tools)
	}
	likec4Validate(t, filepath.Join(out, likec4Dir))
}

// TestGenerateReferenceSystem checks that arch generate refuses the three
// reference controllers, which it cannot compose into one model yet.
func TestGenerateReferenceSystem(t *testing.T) {
	_, err := generateTo(t, "../../examples/refsystem/project.yaml")
	if !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "has 3 controllers") {
		t.Fatalf("expected a refusal to compose three controllers, got %v", err)
	}
}

func TestGenerateWithoutControllers(t *testing.T) {
	out, err := generateTo(t, "testdata/apionly/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, modelFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a project without controllers has no model: %v", err)
	}
	r, err := readReceipt(filepath.Join(out, receiptsFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Generated) != 1 || r.Generated[0].Path != irFile {
		t.Fatalf("receipt does not record ir.yaml: %+v", r.Generated)
	}
	likec4Validate(t, filepath.Join(out, likec4Dir))
}

func TestGenerateFailsClosed(t *testing.T) {
	t.Run("no IR", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run([]string{"--project", configMirror, "--out", t.TempDir(), "generate"}, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "run arch extract first") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("no declaration", func(t *testing.T) {
		_, err := generateTo(t, "../../examples/configmirror/project.yaml")
		if err == nil || !strings.Contains(err.Error(), "has no declaration") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("undeclared client call", func(t *testing.T) {
		out, err := generateTo(t, "testdata/configmirror/undeclared.yaml")
		d, ok := goflow.AsDiagnostic(err)
		if !ok || d.File != "examples/configmirror/controller.go" || d.Line != 30 || !strings.Contains(d.Error(), "client.Client.Update") {
			t.Fatalf("expected a located diagnostic at the Update call, got %v", err)
		}
		if _, err := os.Stat(filepath.Join(out, modelFile)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("no model may be written: %v", err)
		}
	})
}

// likec4Validate runs `likec4 validate` on a workspace directory when
// LIKEC4_BIN is set.
func likec4Validate(t *testing.T, dir string) {
	t.Helper()
	bin := os.Getenv("LIKEC4_BIN")
	if bin == "" {
		t.Log("LIKEC4_BIN not set; skipping LikeC4 validation")
		return
	}
	out, err := exec.Command(bin, "validate", dir).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Valid") {
		t.Fatalf("likec4 validate: %v\n%s", err, out)
	}
}

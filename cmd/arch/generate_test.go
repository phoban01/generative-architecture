package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/report"
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
	t.Run("extract failed closed", func(t *testing.T) {
		out := t.TempDir()
		support := report.Support{Unsupported: []report.Item{
			{Kind: "statement", Construct: "for range jobs", Reason: "range loops are not summarized", Location: report.Location{File: "controller.go", Line: 163}},
			{Kind: "call", Construct: "len(active)", Location: report.Location{File: "controller.go", Line: 170}},
		}}
		if err := support.Write(filepath.Join(out, supportFile)); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		err := run([]string{"--project", configMirror, "--out", out, "generate"}, &stdout, &stderr)
		want := "arch extract failed closed on 2 unsupported constructs, listed in " + filepath.Join(out, supportFile) + "; first: statement for range jobs at controller.go:163"
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "run arch extract first") {
			t.Fatalf("got %v, want %q", err, want)
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

// A return that asks for a delayed requeue reaches the generated model: the
// process reports the after outcome and the runtime schedules the key.
func TestGenerateCarriesRequeue(t *testing.T) {
	out, err := generateTo(t, "testdata/requeue/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	if v, ok := spec.State[outcomeVariable]; !ok || v.Type != "string" {
		t.Fatalf("model has no string %s variable: %+v", outcomeVariable, spec.State[outcomeVariable])
	}
	var after, reportsAfter bool
	for _, a := range spec.Actions {
		if a.Name == eventFinishAfter {
			after = true
		}
		if strings.Contains(string(a.Updates[outcomeVariable]), `"after"`) {
			reportsAfter = true
		}
	}
	if !after || !reportsAfter {
		t.Fatalf("after=%v reportsAfter=%v: the requeue did not reach the model", after, reportsAfter)
	}
}

// The project document's viewHints are applied to the generated workspace:
// the context view carries the hinted title and layout.
func TestGenerateAppliesViewHints(t *testing.T) {
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, likec4Dir, "views", "context.c4"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"title 'ConfigMirror in context'", "autoLayout LeftRight"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("views lack %q:\n%s", want, b)
		}
	}
	r, err := readReceipt(filepath.Join(out, receiptsFile))
	if err != nil {
		t.Fatal(err)
	}
	var recorded bool
	for _, f := range r.Files {
		if strings.HasSuffix(f.Path, "hints.yaml") {
			recorded = true
		}
	}
	if !recorded {
		t.Errorf("the receipt does not record the hints document: %+v", r.Files)
	}
}

// A temporal property over an unknown symbol fails arch generate, not only
// arch check, so a bad constraints document never reaches sampling.
func TestGenerateValidatesTemporal(t *testing.T) {
	_, err := generateTo(t, "testdata/configmirror/bad-temporal.yaml")
	want := "bad-temporal-constraints.yaml: temporal reachesMissing: eventually: line 1, column 1: unknown CEL symbol missing"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %q", err, want)
	}
}

// The generated model declares one domain per resource with the IDs of its
// declared objects, so a constraint can quantify over the population.
func TestGenerateDeclaresPopulations(t *testing.T) {
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	if got := spec.Domains["ConfigMap_objects"]; !reflect.DeepEqual(got, []string{"source", "copy"}) {
		t.Fatalf("ConfigMap_objects = %v, want the declared object IDs", got)
	}
	spec.Properties = append(spec.Properties, model.Property{Name: "namedObjects", Expression: `ConfigMap_objects.all(o, o != "")`})
	if _, err := model.Compile(spec); err != nil {
		t.Fatalf("a quantifier over the population does not compile: %v", err)
	}
}

// A selected field under a quoted map key generates a model whose variables
// use the field's name, and the Go read and write of the key match it.
func TestGenerateQuotedMapKey(t *testing.T) {
	out, err := generateTo(t, "testdata/keyed/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(out, specFile), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ConfigMap_source_appKey", "ConfigMap_copy_appKey"} {
		if _, ok := spec.State[name]; !ok {
			t.Errorf("model lacks %s: %v", name, slices.Sorted(maps.Keys(spec.State)))
		}
	}
	for name := range spec.State {
		if strings.Contains(name, "app.config") {
			t.Errorf("raw map key in a variable name: %s", name)
		}
	}
	var copied bool
	for _, a := range spec.Actions {
		if a.Updates["target_appKey"] == "source_appKey" {
			copied = true
		}
	}
	if !copied {
		t.Fatalf("the keyed value is not copied between the locals: %+v", spec.Actions)
	}
}

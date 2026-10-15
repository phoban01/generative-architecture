package configmirror

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/mutation"
	"generative-architecture/pkg/receipt"
)

// report is report.yaml as cmd/library-demo writes it.
type report struct {
	Result      string                     `yaml:"result"`
	Model       string                     `yaml:"model"`
	ModelSHA256 string                     `yaml:"modelSha256"`
	Sampling    map[string]int             `yaml:"sampling"`
	Scope       string                     `yaml:"scope"`
	Traces      int                        `yaml:"traces"`
	Transitions int                        `yaml:"transitions"`
	Coverage    map[string]int             `yaml:"coverage"`
	TraceHashes map[string]string          `yaml:"traceHashes"`
	Observed    []string                   `yaml:"observed"`
	Bounds      abstraction.Bounds         `yaml:"bounds"`
	Excludes    []string                   `yaml:"excludes"`
	Mutations   map[string]map[string]bool `yaml:"mutations"`
	Tools       map[string]string          `yaml:"tools"`
	Receipt     string                     `yaml:"receipt"`
}

func readReport(t *testing.T) report {
	t.Helper()
	var r report
	if err := krm.ReadResult(ReportFile, "VerificationReport", &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// The report is about the generated model and the traces on disk, repeats
// the abstraction's bounds and excludes verbatim and shows every mutation
// of the suite detected as expected.
func TestEvidenceReport(t *testing.T) {
	r := readReport(t)
	if r.Result != "passed" || r.Model != ModelFile || r.Receipt != ReceiptFile {
		t.Fatalf("report %+v", r)
	}
	spec, err := Generate(".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte(m.Quint()))); r.ModelSHA256 != want {
		t.Fatalf("report model hash %s, generated model %s; run make library-demo", r.ModelSHA256, want)
	}
	paths, err := filepath.Glob(TraceGlob)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, n := range r.Coverage {
		total += n
	}
	if total != r.Transitions || r.Coverage["writeTarget_ok"] == 0 || r.Coverage["finishError"] == 0 {
		t.Fatalf("report coverage %v over %d transitions", r.Coverage, r.Transitions)
	}
	if r.Traces != len(paths) || len(r.TraceHashes) != len(paths) {
		t.Fatalf("report lists %d traces and %d hashes, %d on disk", r.Traces, len(r.TraceHashes), len(paths))
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(b)); r.TraceHashes[filepath.Base(p)] != got {
			t.Fatalf("%s hash %s, report %s", p, got, r.TraceHashes[filepath.Base(p)])
		}
	}
	abs, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if r.Bounds != abs.Spec.Bounds || !reflect.DeepEqual(r.Excludes, abs.Spec.Excludes) {
		t.Fatalf("report bounds %+v excludes %v, abstraction %+v %v", r.Bounds, r.Excludes, abs.Spec.Bounds, abs.Spec.Excludes)
	}
	suite, err := mutation.Load(MutationsFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Mutations) != len(suite.Spec.Mutations) {
		t.Fatalf("report mutations %v, suite %+v", r.Mutations, suite.Spec.Mutations)
	}
	if escaped := suite.Escaped(mutation.Matrix(r.Mutations)); len(escaped) != 0 {
		t.Fatalf("report shows escaped mutations %v", escaped)
	}
	if !strings.HasPrefix(r.Tools["quint"], "0.33.") {
		t.Fatalf("report tools %v", r.Tools)
	}
}

// The receipt records the input closure of the evidence run: the example's
// packages and their in-module imports, its authored documents, the exact
// tool versions and the abstraction's bounds and excludes. The example's
// own inputs have not drifted since.
func TestEvidenceReceipt(t *testing.T) {
	root := filepath.Join("..", "..")
	rec, err := receipt.Read(ReceiptFile, root)
	if err != nil {
		t.Fatal(err)
	}
	r := readReport(t)
	if !reflect.DeepEqual(rec.Tools, r.Tools) {
		t.Fatalf("receipt tools %v, report tools %v", rec.Tools, r.Tools)
	}
	abs, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	wantBounds := map[string]int{"maxObjectsPerResource": abs.Spec.Bounds.MaxObjectsPerResource, "maxInvocations": abs.Spec.Bounds.MaxInvocations}
	if !reflect.DeepEqual(rec.Bounds, wantBounds) || !reflect.DeepEqual(rec.Excludes, abs.Spec.Excludes) {
		t.Fatalf("receipt bounds %v excludes %v", rec.Bounds, rec.Excludes)
	}
	recorded := map[string]bool{}
	for _, f := range rec.Files {
		recorded[f.Path] = true
	}
	for _, want := range []string{
		"examples/configmirror/controller.go", "examples/configmirror/adapter.go", "examples/configmirror/generate.go",
		"examples/configmirror/generated/projection_generated.go", "examples/configmirror/project.yaml",
		"examples/configmirror/abstraction.yaml", "examples/configmirror/constraints.yaml", "examples/configmirror/mutations.yaml",
		"pkg/summaries/data/get.yaml", "pkg/model/model.go", "go.mod", "go.sum", "cmd/library-demo/main.go",
	} {
		if !recorded[want] {
			t.Errorf("receipt does not record %s", want)
		}
	}
	drift, err := receipt.Verify(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drift {
		if strings.HasPrefix(d.Path, "examples/configmirror/") {
			t.Errorf("%s since the evidence run; run make library-demo", d)
		}
	}
}

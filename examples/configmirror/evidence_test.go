package configmirror

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/mutation"
	"github.com/phoban01/generative-architecture/pkg/receipt"
	"github.com/phoban01/generative-architecture/pkg/report"
)

func readReport(t *testing.T) report.Verification {
	t.Helper()
	r, err := report.ReadVerification(ReportFile)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The report is about the generated model and the traces on disk. It runs
// in strict mode with no finding, embeds the abstraction's bounds and
// excludes in its receipt and shows every mutation of the suite detected
// as expected.
func TestEvidenceReport(t *testing.T) {
	r := readReport(t)
	if r.Result != report.Passed || r.Mode != report.ModeStrict || r.Model != ModelFile || len(r.Blocking()) != 0 || len(r.Failures) != len(report.Categories) {
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
	wantBounds := map[string]int{"maxObjectsPerResource": abs.Spec.Bounds.MaxObjectsPerResource, "maxInvocations": abs.Spec.Bounds.MaxInvocations}
	if !reflect.DeepEqual(r.Receipt.Bounds, wantBounds) || !reflect.DeepEqual(r.Receipt.Excludes, abs.Spec.Excludes) {
		t.Fatalf("report bounds %v excludes %v, abstraction %+v %v", r.Receipt.Bounds, r.Receipt.Excludes, abs.Spec.Bounds, abs.Spec.Excludes)
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
	if !strings.HasPrefix(r.Receipt.Tools["quint"], "0.33.") {
		t.Fatalf("report tools %v", r.Receipt.Tools)
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
	embedded := rec
	embedded.Root = ""
	if !reflect.DeepEqual(embedded, r.Receipt) {
		t.Fatalf("the report embeds a receipt other than %s", ReceiptFile)
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

package receipt

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const root = "../.."

var (
	patterns = []string{"./examples/configmirror"}
	extra    = []string{"examples/configmirror/abstraction.yaml"}
	tools    = map[string]string{"quint": "0.33.1", "likec4": "1.59.2"}
)

func collect(t *testing.T) Receipt {
	t.Helper()
	r, err := Collect(root, patterns, extra, tools)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func has(r Receipt, path string) bool {
	for _, f := range r.Files {
		if f.Path == path {
			return true
		}
	}
	return false
}

// copyClosure writes every recorded file into a fresh directory so the
// receipt can be verified against an independent checkout.
func copyClosure(t *testing.T, r Receipt) Receipt {
	t.Helper()
	dir := t.TempDir()
	for _, f := range r.Files {
		src := filepath.Join(r.Root, filepath.FromSlash(f.Path))
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(dst, b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	copied := r
	copied.Root = dir
	return copied
}

func TestCollectClosure(t *testing.T) {
	r := collect(t)
	if r.Module != "github.com/phoban01/generative-architecture" {
		t.Fatalf("module %q", r.Module)
	}
	for _, want := range []string{"examples/configmirror/controller.go", "examples/configmirror/adapter.go", "pkg/conformance/replay.go", "pkg/controllerruntime/worker.go", "pkg/krm/resource.go", "pkg/model/model.go", "pkg/expr/cel.go", "go.mod", "go.sum", "examples/configmirror/abstraction.yaml", "pkg/summaries/data/get.yaml"} {
		if !has(r, want) {
			t.Errorf("closure lacks %s", want)
		}
	}
	for _, f := range r.Files {
		if strings.HasSuffix(f.Path, "_test.go") || strings.HasPrefix(f.Path, "../") || filepath.IsAbs(f.Path) || len(f.SHA256) != 64 {
			t.Errorf("unexpected entry %+v", f)
		}
	}
	if has(r, "examples/database/controller.go") || has(r, "pkg/project/project.go") {
		t.Fatalf("closure includes packages the example does not import")
	}
	if !reflect.DeepEqual(r.Tools, tools) || !reflect.DeepEqual(r.Packages, patterns) {
		t.Fatalf("tools %v packages %v", r.Tools, r.Packages)
	}
	drift, err := Verify(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 {
		t.Fatalf("unexpected drift %v", drift)
	}
}

func TestVerifySeesDrift(t *testing.T) {
	copied := copyClosure(t, collect(t))
	drift, err := Verify(copied)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 {
		t.Fatalf("fresh copy drifted: %v", drift)
	}
	controller := filepath.Join(copied.Root, "examples", "configmirror", "controller.go")
	b, err := os.ReadFile(controller)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(controller, append(b, []byte("\n// drift\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(copied.Root, "examples", "configmirror", "abstraction.yaml")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(copied.Root, "examples", "configmirror", "extra.go"), []byte("package configmirror\n"), 0644); err != nil {
		t.Fatal(err)
	}
	drift, err = Verify(copied)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"examples/configmirror/extra.go added",
		"examples/configmirror/controller.go changed",
		"examples/configmirror/abstraction.yaml missing",
	}
	if len(drift) != len(want) {
		t.Fatalf("drift %v, want %v", drift, want)
	}
	for i, d := range drift {
		if !strings.HasPrefix(d.String(), want[i]) {
			t.Fatalf("drift %d: %s, want %s", i, d, want[i])
		}
	}
	if drift[1].Kind != Changed || drift[1].Want == drift[1].Got || drift[1].Got == "" {
		t.Fatalf("changed entry %+v", drift[1])
	}
}

func TestCompareTools(t *testing.T) {
	r := collect(t)
	later := r
	later.Tools = map[string]string{"quint": "0.33.2", "node": "24.21.0"}
	drift := Compare(r, later)
	if len(drift) != 3 || drift[0].Kind != Tool || drift[0].Path != "likec4" || drift[0].Got != "" || drift[1].Path != "node" || drift[2].Path != "quint" || drift[2].Want != "0.33.1" || drift[2].Got != "0.33.2" {
		t.Fatalf("drift %v", drift)
	}
}

func TestWriteRead(t *testing.T) {
	r := collect(t)
	r.Bounds = map[string]int{"maxObjectsPerResource": 2, "maxInvocations": 3}
	r.Excludes = []string{"delete", "list"}
	path := filepath.Join(t.TempDir(), "receipt.yaml")
	if err := Write(path, "config-mirror", r); err != nil {
		t.Fatal(err)
	}
	back, err := Read(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if back.Root != r.Root || !reflect.DeepEqual(back.Files, r.Files) || !reflect.DeepEqual(back.Tools, r.Tools) || !reflect.DeepEqual(back.Packages, r.Packages) || !reflect.DeepEqual(back.Extra, r.Extra) || back.Module != r.Module || !reflect.DeepEqual(back.Bounds, r.Bounds) || !reflect.DeepEqual(back.Excludes, r.Excludes) {
		t.Fatalf("round trip changed the receipt:\n%+v\n%+v", r, back)
	}
	bad := r
	bad.Bounds = map[string]int{"maxInvocations": 0}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("non-positive bound accepted: %v", err)
	}
	if drift, err := Verify(back); err != nil || len(drift) != 0 {
		t.Fatalf("drift %v, err %v", drift, err)
	}
	if _, err = Read(filepath.Join(root, "examples", "configmirror", "report.yaml"), root); err == nil {
		t.Fatal("VerificationReport accepted as EvidenceReceipt")
	}
}

func TestCollectRejectsInexactVersionsAndMissingInputs(t *testing.T) {
	for name, version := range map[string]string{"range": ">=0.33.0", "wildcard": "0.33.x ", "empty": ""} {
		if _, err := Collect(root, patterns, nil, map[string]string{"quint": version}); err == nil || !strings.Contains(err.Error(), "exact version") {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if _, err := Collect(root, patterns, []string{"examples/configmirror/absent.yaml"}, nil); err == nil || !strings.Contains(err.Error(), "absent.yaml") {
		t.Errorf("missing extra: got %v", err)
	}
	if _, err := Collect(root, nil, nil, nil); err == nil {
		t.Error("no packages accepted")
	}
	if _, err := Verify(Receipt{}); err == nil {
		t.Error("verify without root accepted")
	}
}

func TestSamplingValidate(t *testing.T) {
	for name, c := range map[string]struct {
		s  Sampling
		ok bool
	}{
		"uniform":          {Sampling{Samples: 40, Steps: 60, Seed: 1, Strategy: "uniform"}, true},
		"weighted":         {Sampling{Samples: 40, Steps: 60, Strategy: "weighted", Weights: map[string]int{"a": 8}}, true},
		"no strategy":      {Sampling{Samples: 40, Steps: 60}, false},
		"unknown strategy": {Sampling{Samples: 40, Steps: 60, Strategy: "exhaustive"}, false},
		"zero samples":     {Sampling{Steps: 60, Strategy: "uniform"}, false},
		"uniform weights":  {Sampling{Samples: 1, Steps: 1, Strategy: "uniform", Weights: map[string]int{"a": 2}}, false},
		"weighted no map":  {Sampling{Samples: 1, Steps: 1, Strategy: "weighted"}, false},
		"weighted zero":    {Sampling{Samples: 1, Steps: 1, Strategy: "weighted", Weights: map[string]int{"a": 0}}, false},
		"weighted unnamed": {Sampling{Samples: 1, Steps: 1, Strategy: "weighted", Weights: map[string]int{"": 2}}, false},
	} {
		if err := c.s.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate() = %v", name, err)
		}
		r := Receipt{Module: "m", Packages: []string{"./..."}, Files: []File{{Path: "a.go", SHA256: strings.Repeat("0", 64)}}, Tools: map[string]string{"go": "go1.26.8"}, Sampling: &c.s}
		if err := r.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: receipt Validate() = %v", name, err)
		}
	}
}

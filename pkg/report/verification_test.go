package report

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/receipt"
)

func sampleVerification() Verification {
	return Verification{
		Name: "sample", Mode: ModeInventory, Scope: "bounded", Model: "Model.qnt",
		Receipt: receipt.Receipt{Module: "example.org/m", Packages: []string{"./..."}, Files: []receipt.File{{Path: "a.go", SHA256: strings.Repeat("0", 64)}}, Tools: map[string]string{"quint": "0.33.0"}, Bounds: map[string]int{"maxInvocations": 3}},
	}
}

func TestVerificationConclude(t *testing.T) {
	for want, findings := range map[string][]Finding{
		Passed:     nil,
		Incomplete: {{Category: CategoryUnsupported, Message: "no adapter"}},
		Failed:     {{Category: CategoryUnsupported, Message: "no adapter"}, {Category: CategorySafety, Message: "violated"}},
	} {
		v := sampleVerification()
		v.Findings = findings
		if got := v.Conclude(); got != want {
			t.Errorf("findings %v: got %s, want %s", findings, got, want)
		}
	}
}

// Conclude counts every category, zeros included, and the counts survive a
// write and read.
func TestVerificationFailureCounts(t *testing.T) {
	v := sampleVerification()
	v.Findings = []Finding{
		{Category: CategorySafety, Message: "a"}, {Category: CategorySafety, Message: "b"},
		{Category: CategoryUnsupported, Message: "c"},
	}
	v.Conclude()
	want := map[string]int{CategorySafety: 2, CategoryConformance: 0, CategoryUnsupported: 1, CategoryTool: 0}
	if !reflect.DeepEqual(v.Failures, want) {
		t.Fatalf("failures %v, want %v", v.Failures, want)
	}
	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := v.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadVerification(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Failures, want) || back.Mode != ModeInventory {
		t.Fatalf("read back failures %v mode %q", back.Failures, back.Mode)
	}
	// Hand-edited counts or results that disagree with the findings fail.
	v.Failures[CategorySafety] = 1
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "failure counts") {
		t.Fatalf("wrong count accepted: %v", err)
	}
	v.Conclude()
	v.Result = Passed
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("wrong result accepted: %v", err)
	}
}

// Strict mode blocks on every finding; inventory mode only on findings
// other than unsupported ones.
func TestVerificationBlocking(t *testing.T) {
	unsupported := Finding{Category: CategoryUnsupported, Message: "no adapter"}
	tool := Finding{Category: CategoryTool, Message: "quint missing"}
	for _, tc := range []struct {
		mode     string
		findings []Finding
		want     []Finding
	}{
		{ModeInventory, []Finding{unsupported}, nil},
		{ModeStrict, []Finding{unsupported}, []Finding{unsupported}},
		{ModeInventory, []Finding{unsupported, tool}, []Finding{tool}},
		{ModeStrict, []Finding{unsupported, tool}, []Finding{tool, unsupported}},
	} {
		v := sampleVerification()
		v.Mode, v.Findings = tc.mode, tc.findings
		if got := v.Blocking(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v: blocking %v, want %v", tc.mode, tc.findings, got, tc.want)
		}
	}
	if ModeOf(true) != ModeStrict || ModeOf(false) != ModeInventory {
		t.Fatal("ModeOf")
	}
}

func TestVerificationRoundTrip(t *testing.T) {
	v := sampleVerification()
	v.Findings = []Finding{{Category: CategoryConformance, Trace: "trace-000.itf.json", Step: 4, Event: "begin", Message: "disagree"}}
	v.Conclude()
	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := v.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadVerification(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Result != Failed || back.Findings[0].Step != 4 || back.Receipt.Tools["quint"] != "0.33.0" || back.Receipt.Bounds["maxInvocations"] != 3 {
		t.Fatalf("round trip %+v", back)
	}
	v.Findings[0].Category = "maybe"
	if err := v.Write(path); err == nil {
		t.Fatal("unknown category accepted")
	}
	v.Result = "fine"
	if err := v.Validate(); err == nil {
		t.Fatal("unknown result accepted")
	}
	v = sampleVerification()
	v.Conclude()
	v.Mode = ""
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("missing mode accepted: %v", err)
	}
}

// A run without a receipt records its inputs by hash. The receipt is then
// left out of the document.
func TestVerificationWithoutReceipt(t *testing.T) {
	v := Verification{Name: "example", Mode: ModeStrict, Scope: "bounded", InputHashes: map[string]string{"a.go": strings.Repeat("1", 64)}, Observed: []string{"x"}, Mutations: map[string]map[string]bool{"m": {"x": true}}}
	v.Conclude()
	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := v.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadVerification(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.InputHashes["a.go"] == "" || !back.Mutations["m"]["x"] || len(back.Receipt.Files) != 0 {
		t.Fatalf("round trip %+v", back)
	}
}

// Warnings are recorded and listed, but they do not change the result or
// block the run in either mode (ADR-0009).
func TestVerificationWarningsDoNotBlock(t *testing.T) {
	v := sampleVerification()
	v.Warnings = []string{"library input pkg/model/model.go changed (000000000000 -> 111111111111)"}
	for _, mode := range []string{ModeStrict, ModeInventory} {
		v.Mode = mode
		if v.Conclude() != Passed || len(v.Blocking()) != 0 || v.Failures[CategoryTool] != 0 {
			t.Fatalf("%s: result %s, blocking %v, failures %v", mode, v.Result, v.Blocking(), v.Failures)
		}
	}
	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := v.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadVerification(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Warnings, v.Warnings) || back.Result != Passed {
		t.Fatalf("round trip warnings %v result %s", back.Warnings, back.Result)
	}
	var b strings.Builder
	back.WriteSections(&b)
	if want := "warnings (not blocking):\n  " + v.Warnings[0] + "\n"; b.String() != want {
		t.Fatalf("sections %q, want %q", b.String(), want)
	}
	v.Warnings = []string{""}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "empty warning") {
		t.Fatalf("empty warning accepted: %v", err)
	}
}

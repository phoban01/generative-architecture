package report

import (
	"path/filepath"
	"strings"
	"testing"

	"generative-architecture/pkg/receipt"
)

func sampleVerification() Verification {
	return Verification{
		Name: "sample", Scope: "bounded", Model: "Model.qnt",
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
}

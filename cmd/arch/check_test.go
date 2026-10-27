package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/report"
)

// savedTraces are the traces committed with the ConfigMirror sample,
// sampled from the model arch generate reproduces.
const savedTraces = "../../examples/configmirror/traces/*.itf.json"

// arch runs one command against a project and output directory.
func arch(t *testing.T, projectPath, out string, strict bool, args ...string) (string, error) {
	t.Helper()
	all := []string{"--project", projectPath, "--out", out}
	if strict {
		all = append(all, "--strict")
	}
	var stdout, stderr bytes.Buffer
	err := run(append(all, args...), &stdout, &stderr)
	return stderr.String(), err
}

func readVerification(t *testing.T, path string) report.Verification {
	t.Helper()
	v, err := report.ReadVerification(path)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestReplaySavedConfigMirrorTraces(t *testing.T) {
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := arch(t, configMirror, out, false, "replay", "--traces", savedTraces)
	if err != nil {
		t.Fatalf("inventory mode must pass: %v", err)
	}
	if !strings.Contains(stderr, "inventory mode") {
		t.Fatalf("missing inventory note: %q", stderr)
	}
	v := readVerification(t, filepath.Join(out, replayReport))
	if v.Result != report.Incomplete || v.Traces != 40 || v.Transitions == 0 || len(v.TraceHashes) != 40 {
		t.Fatalf("report %s: %d traces, %d transitions", v.Result, v.Traces, v.Transitions)
	}
	if len(v.Findings) != 1 || v.Findings[0].Category != report.CategoryUnsupported || !strings.HasPrefix(v.Findings[0].Message, "no adapter") {
		t.Fatalf("findings %+v", v.Findings)
	}
	if v.Receipt.Tools["go"] == "" || v.Receipt.Bounds["maxInvocations"] != 3 || len(v.Generated) != 1 {
		t.Fatalf("report does not embed the receipt: %+v %+v", v.Receipt.Tools, v.Receipt.Bounds)
	}
	if v.Mode != report.ModeInventory || v.Failures[report.CategoryUnsupported] != 1 || v.Failures[report.CategorySafety] != 0 {
		t.Fatalf("mode %q failures %v", v.Mode, v.Failures)
	}
	if _, err := arch(t, configMirror, out, true, "replay", "--traces", savedTraces); !errors.Is(err, errStrict) {
		t.Fatalf("verification mode must refuse the missing adapter, got %v", err)
	}
	if v := readVerification(t, filepath.Join(out, replayReport)); v.Mode != report.ModeStrict || len(v.Blocking()) != 1 {
		t.Fatalf("strict report mode %q blocking %v", v.Mode, v.Blocking())
	}
}

func TestReplayReportsSafetyViolation(t *testing.T) {
	// The same model with boundedRetries tightened to retries <= 0.
	const violated = "testdata/configmirror/violated.yaml"
	out, err := generateTo(t, violated)
	if err != nil {
		t.Fatal(err)
	}
	_, err = arch(t, violated, out, false, "replay", "--traces", savedTraces)
	if err == nil || !strings.Contains(err.Error(), "safety finding") {
		t.Fatalf("expected a safety finding, got %v", err)
	}
	v := readVerification(t, filepath.Join(out, replayReport))
	var safety []report.Finding
	for _, f := range v.Findings {
		if f.Category == report.CategorySafety {
			safety = append(safety, f)
		}
	}
	if v.Result != report.Failed || len(safety) == 0 || safety[0].Trace == "" || safety[0].Step == 0 || !strings.Contains(safety[0].Message, "boundedRetries") {
		t.Fatalf("report %s, safety findings %+v", v.Result, safety)
	}
}

func TestReplayRefusesChangedInputs(t *testing.T) {
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(out, irFile), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# edited\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := arch(t, configMirror, out, false, "replay", "--traces", savedTraces); err == nil || !strings.Contains(err.Error(), "tool finding: generated input ir.yaml changed") {
		t.Fatalf("expected a tool finding for the changed IR, got %v", err)
	}
}

func TestReplayModelCategories(t *testing.T) {
	spec := model.Spec{Name: "Counter", State: map[string]model.Variable{"n": {Type: "int", Initial: "0"}},
		Actions:    []model.Action{{Name: "inc", Guard: "n < 3", Updates: map[string]expr.Expression{"n": "n + 1"}}},
		Properties: []model.Property{{Name: "small", Expression: "n < 2"}}}
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	state := func(event string, n int64) conformance.State {
		return conformance.State{Event: event, Values: map[string]any{"n": n}}
	}
	cases := map[string]struct {
		trace conformance.Trace
		want  string
		step  int
	}{
		"conforms":      {conformance.Trace{States: []conformance.State{state("init", 0), state("inc", 1)}}, "", 0},
		"wrong value":   {conformance.Trace{States: []conformance.State{state("init", 0), state("inc", 2)}}, report.CategoryConformance, 1},
		"unknown event": {conformance.Trace{States: []conformance.State{state("init", 0), state("dec", 1)}}, report.CategoryConformance, 1},
		"violation":     {conformance.Trace{States: []conformance.State{state("init", 0), state("inc", 1), state("inc", 2)}}, report.CategorySafety, 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, f := replayModel(m, "t", tc.trace, map[string]int{})
			switch {
			case tc.want == "" && f != nil:
				t.Fatalf("unexpected finding %+v", f)
			case tc.want != "" && (f == nil || f.Category != tc.want || f.Step != tc.step):
				t.Fatalf("got %+v, want %s at step %d", f, tc.want, tc.step)
			}
		})
	}
}

func TestCheckConfigMirror(t *testing.T) {
	if os.Getenv("QUINT_BIN") == "" {
		t.Skip("QUINT_BIN not set")
	}
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arch(t, configMirror, out, true, "check", "--samples", "5", "--steps", "30", "--seed", "7"); err != nil {
		t.Fatal(err)
	}
	v := readVerification(t, filepath.Join(out, checkReport))
	if v.Result != report.Passed || v.Traces != 5 || v.Transitions == 0 || v.Sampling == nil || v.Sampling.Seed != 7 {
		t.Fatalf("report %+v", v)
	}
	if v.Receipt.Tools["quint"] == "" || v.Receipt.Bounds["maxInvocations"] != 3 {
		t.Fatalf("receipt tools %v bounds %v", v.Receipt.Tools, v.Receipt.Bounds)
	}
	// arch replay picks up the sampled traces.
	if _, err := arch(t, configMirror, out, false, "replay"); err != nil {
		t.Fatal(err)
	}
	if r := readVerification(t, filepath.Join(out, replayReport)); r.Traces != 5 {
		t.Fatalf("replay read %d traces", r.Traces)
	}
	// A mutation suite is listed, not run.
	if _, err := arch(t, configMirror, out, false, "check", "--samples", "2", "--steps", "10", "--mutations", "../../examples/configmirror/mutations.yaml"); err != nil {
		t.Fatal(err)
	}
	v = readVerification(t, filepath.Join(out, checkReport))
	if v.Result != report.Incomplete || len(v.Findings) != 2 || !strings.Contains(v.Findings[0].Message, "direct-source") {
		t.Fatalf("mutation findings %+v", v.Findings)
	}
}

func TestCheckReportsSafetyViolation(t *testing.T) {
	if os.Getenv("QUINT_BIN") == "" {
		t.Skip("QUINT_BIN not set")
	}
	const violated = "testdata/configmirror/violated.yaml"
	out, err := generateTo(t, violated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arch(t, violated, out, false, "check", "--samples", "20", "--steps", "60"); err == nil || !strings.Contains(err.Error(), "safety finding") {
		t.Fatalf("expected a safety finding, got %v", err)
	}
	if v := readVerification(t, filepath.Join(out, checkReport)); v.Result != report.Failed || v.Findings[0].Category != report.CategorySafety {
		t.Fatalf("report %s findings %+v", v.Result, v.Findings)
	}
}

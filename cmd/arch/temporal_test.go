package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/project"
	"generative-architecture/pkg/quint"
	"generative-architecture/pkg/report"
)

// counterEvidence is evidence for a model that counts from 0 to 3 and may
// stutter at any count.
func counterEvidence(t *testing.T) evidence {
	t.Helper()
	m, err := model.Compile(model.Spec{
		Name:    "Counter",
		State:   map[string]model.Variable{"count": {Type: "int", Initial: "0"}},
		Actions: []model.Action{{Name: "increment", Guard: "count < 3", Updates: map[string]expr.Expression{"count": "count + 1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return evidence{model: m}
}

var (
	reachesThree   = model.Temporal{Name: "reachesThree", Eventually: "count == 3"}
	oneLeadsToZero = model.Temporal{Name: "oneLeadsToZero", LeadsTo: &model.LeadsTo{From: "count == 1", To: "count == 0"}}
	fairIncrement  = model.Fairness{Weak: []string{"increment"}}
)

func TestTemporalUnchecked(t *testing.T) {
	ev := counterEvidence(t)
	fake := filepath.Join(t.TempDir(), "quint")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.33.0; exit 0; fi\necho 'exec: java: not found' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		constraints model.Constraints
		reason      string
		want        string
	}{
		"without --verify": {model.Constraints{Temporal: []model.Temporal{reachesThree}, Fairness: fairIncrement}, "not model-checked: no --verify", "no --verify"},
		"without fairness": {model.Constraints{Temporal: []model.Temporal{reachesThree}}, "", "no fairness assumption declared"},
		"checker fails":    {model.Constraints{Temporal: []model.Temporal{reachesThree}, Fairness: fairIncrement}, "", "java: not found"},
	} {
		results, findings := checkTemporal(ev, tc.constraints, tc.reason, quint.Options{Executable: fake})
		if len(results) != 1 || results[0].Status != report.TemporalUnchecked || !strings.Contains(results[0].Reason, tc.want) {
			t.Errorf("%s: %+v", name, results)
		}
		if len(findings) != 1 || findings[0].Category != report.CategoryUnsupported || findings[0].Property != "reachesThree" {
			t.Errorf("%s: findings %+v", name, findings)
		}
	}
}

// TestTemporalVerified runs quint verify with TLC when QUINT_BIN and Java
// are available.
func TestTemporalVerified(t *testing.T) {
	exe := os.Getenv("QUINT_BIN")
	if exe == "" {
		t.Skip("QUINT_BIN unset")
	}
	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("java not on PATH: quint verify with TLC needs a Java 21 runtime")
	}
	results, findings := checkTemporal(counterEvidence(t), model.Constraints{Temporal: []model.Temporal{reachesThree, oneLeadsToZero}, Fairness: fairIncrement}, "", quint.Options{Executable: exe})
	if len(results) != 2 || results[0].Status != report.TemporalVerified || results[1].Status != report.TemporalViolated {
		t.Fatalf("%+v", results)
	}
	if !strings.Contains(results[0].Bound, "complete reachable state space") || !strings.Contains(results[0].Backend, "--backend tlc") {
		t.Fatalf("%+v", results[0])
	}
	if len(findings) != 1 || findings[0].Category != report.CategorySafety || findings[0].Property != "oneLeadsToZero" || !strings.Contains(findings[0].Message, "weak(increment)") {
		t.Fatalf("%+v", findings)
	}
}

func TestLoadTemporalFailsClosed(t *testing.T) {
	ev := counterEvidence(t)
	dir := t.TempDir()
	ev.project = project.Project{Dir: dir, Spec: project.Spec{Constraints: []string{"constraints.yaml"}}}
	write := func(spec string) {
		doc := "apiVersion: architecture.local/v1alpha1\nkind: ModelConstraints\nmetadata:\n  name: sample\nspec:\n" + spec
		if err := os.WriteFile(filepath.Join(dir, "constraints.yaml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("  temporal:\n    - name: reachesThree\n      eventually: 'count == 3'\n")
	c, err := loadTemporal(ev)
	if err != nil || len(c.Temporal) != 1 || !c.Fairness.Empty() {
		t.Fatalf("a property without fairness loads and is reported unchecked: %+v %v", c, err)
	}
	for spec, want := range map[string]string{
		"  temporal:\n    - name: p\n      eventually: 'missing == 3'\n":                               "constraints.yaml: temporal p: eventually: line 1, column 1: unknown CEL symbol missing",
		"  temporal:\n    - name: p\n      eventually: 'count == 3'\n  fairness:\n    weak: [reset]\n": `unknown action "reset"`,
		"  temporal:\n    - {name: p, eventually: 'true'}\n    - {name: p, eventually: 'true'}\n":      "temporal p: declared twice",
		"  temporal:\n    - name: p\n      always: 'true'\n":                                           "field always not found",
	} {
		write(spec)
		if _, err := loadTemporal(ev); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", spec, err, want)
		}
	}
}

// TestCheckReportsTemporalUnchecked runs arch check on ConfigMirror with
// temporal properties and no --verify. Whatever sampling finds, each
// temporal property is unchecked, never verified.
func TestCheckReportsTemporalUnchecked(t *testing.T) {
	const temporal = "testdata/configmirror/temporal.yaml"
	out, err := generateTo(t, temporal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arch(t, temporal, out, false, "check", "--samples", "2", "--steps", "10"); err != nil && os.Getenv("QUINT_BIN") != "" {
		t.Fatal(err)
	}
	v := readVerification(t, filepath.Join(out, checkReport))
	if len(v.Temporal) != 2 || v.Result == report.Passed || !strings.Contains(v.Scope, "never checked by sampling") {
		t.Fatalf("%s %+v", v.Result, v.Temporal)
	}
	unchecked := 0
	for _, f := range v.Findings {
		if f.Category == report.CategoryUnsupported && strings.HasPrefix(f.Message, "temporal property ") {
			unchecked++
		}
	}
	for _, r := range v.Temporal {
		if r.Status != report.TemporalUnchecked || !strings.Contains(r.Reason, "--verify") || strings.Join(r.Weak, ",") != "begin,finish" {
			t.Fatalf("%+v", r)
		}
	}
	if unchecked != 2 {
		t.Fatalf("findings %+v", v.Findings)
	}
}

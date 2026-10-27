package model_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
)

func TestTemporalModule(t *testing.T) {
	m, err := model.Compile(counterSpec())
	if err != nil {
		t.Fatal(err)
	}
	fair := model.Fairness{Weak: []string{"increment"}}
	src, err := m.TemporalModule(model.Temporal{Name: "reachesThree", Eventually: "count == 3"}, fair)
	if err != nil {
		t.Fatal(err)
	}
	want := ` temporal reachesThree = (weakFair(increment, (count, event))) implies eventually((count == 3))` + "\n}\n"
	if !strings.HasSuffix(src, want) || !strings.HasPrefix(src, strings.TrimSuffix(m.Quint(), "}\n")) {
		t.Fatalf("got\n%s\nwant suffix\n%s", src, want)
	}
	src, err = m.TemporalModule(model.Temporal{Name: "oneLeadsToTwo", LeadsTo: &model.LeadsTo{From: "count == 1", To: "count == 2"}}, model.Fairness{Strong: []string{"increment"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, ` temporal oneLeadsToTwo = (strongFair(increment, (count, event))) implies always((count == 1) implies eventually((count == 2)))`) {
		t.Fatal(src)
	}
	if fair.String() != "weak(increment)" || (model.Fairness{}).String() != "none" {
		t.Fatal(fair.String())
	}
}

func TestTemporalModuleFailsClosed(t *testing.T) {
	m, err := model.Compile(counterSpec())
	if err != nil {
		t.Fatal(err)
	}
	fair := model.Fairness{Weak: []string{"increment"}}
	for _, tc := range []struct {
		name     string
		temporal model.Temporal
		fair     model.Fairness
		message  string
	}{
		{"no fairness", model.Temporal{Name: "p", Eventually: "count == 3"}, model.Fairness{}, "no fairness assumption declared"},
		{"unknown action", model.Temporal{Name: "p", Eventually: "count == 3"}, model.Fairness{Weak: []string{"reset"}}, `unknown action "reset"`},
		{"repeated action", model.Temporal{Name: "p", Eventually: "count == 3"}, model.Fairness{Weak: []string{"increment"}, Strong: []string{"increment"}}, "listed twice"},
		{"name clash", model.Temporal{Name: "bounded", Eventually: "count == 3"}, fair, "duplicate model identifier"},
		{"reserved name", model.Temporal{Name: "step", Eventually: "count == 3"}, fair, "duplicate model identifier"},
		{"no formula", model.Temporal{Name: "p"}, fair, "set one of eventually and leadsTo"},
		{"two formulas", model.Temporal{Name: "p", Eventually: "true", LeadsTo: &model.LeadsTo{From: "true", To: "true"}}, fair, "not both"},
		{"empty leadsTo", model.Temporal{Name: "p", LeadsTo: &model.LeadsTo{From: "true"}}, fair, "leadsTo.to: predicate required"},
		{"invalid without fairness", model.Temporal{Name: "p", Eventually: "count"}, model.Fairness{}, "eventually: CEL type mismatch"},
		{"non-boolean", model.Temporal{Name: "p", Eventually: "count"}, fair, "eventually: CEL type mismatch"},
		{"undeclared domain", model.Temporal{Name: "p", Eventually: `Pods.exists(x, x == "a")`}, fair, "eventually: line 1, column 1: exists() ranges over Pods"},
	} {
		_, err := m.TemporalModule(tc.temporal, tc.fair)
		if err == nil || !strings.Contains(err.Error(), tc.message) || !strings.HasPrefix(err.Error(), "temporal "+tc.temporal.Name+": ") {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.message)
		}
		if tc.name == "no fairness" && !errors.Is(err, model.ErrNoFairness) {
			t.Errorf("no fairness: %v is not ErrNoFairness", err)
		}
	}
}

func TestLoadConstraintsWithTemporal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "constraints.yaml")
	doc := `apiVersion: architecture.local/v1alpha1
kind: ModelConstraints
metadata:
  name: sample
spec:
  properties:
    - name: bounded
      expression: 'count <= 3'
  temporal:
    - name: reachesThree
      eventually: 'count == 3'
    - name: oneLeadsToTwo
      leadsTo: {from: 'count == 1', to: 'count == 2'}
  fairness:
    weak: [increment]
`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	var c model.Constraints
	if _, err := krm.Read(path, "ModelConstraints", &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Properties) != 1 || len(c.Temporal) != 2 || c.Temporal[0].Eventually != "count == 3" || c.Temporal[1].LeadsTo.To != "count == 2" || c.Fairness.String() != "weak(increment)" {
		t.Fatalf("%+v", c)
	}
}

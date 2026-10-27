package model_test

import (
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/model"
)

func TestDeclaredDomains(t *testing.T) {
	s := model.Spec{
		Name:       "Keys",
		Domains:    map[string][]string{"Keys": {"a", "b"}},
		State:      map[string]model.Variable{"chosen": {Type: "string", Initial: `"c"`}, "member": {Type: "bool", Initial: "true"}},
		Actions:    []model.Action{{Name: "observe", Guard: `Keys.all(k, k != "")`, Updates: map[string]expr.Expression{"member": "Keys.exists(k, k == chosen)"}}},
		Properties: []model.Property{{Name: "known", Expression: `Keys.exists(k, k == "a")`}},
	}
	m, err := model.Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`pure val Keys: Set[str] = Set("a", "b")`, `member' = Keys.exists(k => (k == chosen))`, `val known = Keys.exists(k => (k == "a"))`} {
		if !strings.Contains(m.Quint(), want) {
			t.Fatalf("Quint lacks %q:\n%s", want, m.Quint())
		}
	}
	state, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	// The caller's domain cannot change the compiled oracle.
	s.Domains["Keys"][0] = "c"
	next, err := m.Next(state, "observe")
	if err != nil {
		t.Fatal(err)
	}
	if next["member"] != false || len(next) != 2 {
		t.Fatalf("domains are constants, not state: %v", next)
	}
	if err := m.Check(next, "observe"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsInvalidDomains(t *testing.T) {
	for name, domains := range map[string]map[string][]string{
		"empty":     {"Keys": nil},
		"duplicate": {"Keys": {"a", "a"}},
		"collision": {"count": {"a"}},
		"reserved":  {"all": {"a"}},
	} {
		s := counterSpec()
		s.Domains = domains
		if _, err := model.Compile(s); err == nil {
			t.Errorf("%s domain accepted", name)
		}
	}
	undeclared := counterSpec()
	undeclared.Properties[0].Expression = `Pods.exists(p, p == "a")`
	_, err := model.Compile(undeclared)
	if err == nil || !strings.Contains(err.Error(), "property bounded: line 1, column 1: exists() ranges over Pods, which is not a declared domain") {
		t.Fatalf("got %v", err)
	}
}

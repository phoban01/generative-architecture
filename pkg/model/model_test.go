package model_test

import (
	"context"
	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/model"
	"strings"
	"testing"
)

func counterSpec() model.Spec {
	return model.Spec{Name: "Counter", State: map[string]model.Variable{"count": {Type: "int", Initial: "0"}}, Actions: []model.Action{{Name: "increment", Guard: "count < 3", Updates: map[string]expr.Expression{"count": "count + 1"}}}, Properties: []model.Property{{Name: "bounded", Expression: "count <= 3"}}}
}
func TestDomainNeutralRecordsAndSimultaneousUpdates(t *testing.T) {
	s := model.Spec{Name: "Temperature", Records: map[string]map[string]string{"Reading": {"degrees": "int", "valid": "bool"}}, State: map[string]model.Variable{"reading": {Type: "Reading", Initial: `{"degrees": 10, "valid": true}`}, "previous": {Type: "int", Initial: "0"}}, Actions: []model.Action{{Name: "warm", Guard: "reading.valid", Updates: map[string]expr.Expression{"reading": `{"degrees": reading.degrees + 1, "valid": true}`, "previous": "reading.degrees"}}}}
	m, err := model.Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	state, err = m.Next(state, "warm")
	if err != nil {
		t.Fatal(err)
	}
	if state["previous"] != int64(10) || state["reading"].(map[string]any)["degrees"] != int64(11) {
		t.Fatal(state)
	}
	// Mutating the caller's schema cannot silently change the compiled oracle.
	s.Actions[0].Updates["previous"] = "99"
	again, err := m.Next(state, "warm")
	if err != nil {
		t.Fatal(err)
	}
	if again["previous"] != int64(11) {
		t.Fatal("compiled model aliases caller input")
	}
}
func TestRejectsUnsupportedOrAmbiguousSemantics(t *testing.T) {
	for name, mutate := range map[string]func(*model.Spec){
		"unknown effect":    func(s *model.Spec) { s.Actions[0].Effect = "network" },
		"wrong update type": func(s *model.Spec) { s.Actions[0].Updates["count"] = `"x"` },
		"unknown state":     func(s *model.Spec) { s.Actions[0].Updates["other"] = "1" },
		"unsupported CEL":   func(s *model.Spec) { s.Actions[0].Guard = `"x".startsWith("y")` },
		"name collision":    func(s *model.Spec) { s.Actions[0].Name = "count" },
		"invalid property":  func(s *model.Spec) { s.Properties[0].Expression = "count" },
	} {
		t.Run(name, func(t *testing.T) {
			s := counterSpec()
			mutate(&s)
			if _, err := model.Compile(s); err == nil {
				t.Fatal("accepted invalid model")
			}
		})
	}
}

type counter struct {
	value  int64
	mutant bool
}

func (c *counter) Apply(context.Context, string) error {
	c.value++
	if c.mutant {
		c.value++
	}
	return nil
}
func (c *counter) Project(context.Context) (map[string]any, error) {
	return map[string]any{"count": c.value}, nil
}
func TestIndependentReplayRejectsMutantAndAlteredTrace(t *testing.T) {
	m, err := model.Compile(counterSpec())
	if err != nil {
		t.Fatal(err)
	}
	trace, err := conformance.Scenario(m, "increment", "increment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conformance.Replay(context.Background(), m, trace, &counter{}); err != nil {
		t.Fatal(err)
	}
	if _, err = conformance.Replay(context.Background(), m, trace, &counter{mutant: true}); err == nil || !strings.Contains(err.Error(), "conformance mismatch") {
		t.Fatalf("%v", err)
	}
	trace.States[1].Values["count"] = int64(10)
	if _, err = conformance.Replay(context.Background(), m, trace, &counter{}); err == nil || !strings.Contains(err.Error(), "CEL/trace") {
		t.Fatalf("%v", err)
	}
}

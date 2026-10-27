package quint

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
)

func steerModel(t *testing.T) *model.Compiled {
	t.Helper()
	var spec model.Spec
	if _, err := krm.Read(filepath.Join("..", "..", "examples", "configmirror", "generated", "model.yaml"), "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestProcessActions(t *testing.T) {
	m := steerModel(t)
	all, err := ProcessActions(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"node0_return", "node7_branch", "readSource_found", "readSource_error", "writeTarget_ok"} {
		if !slices.Contains(all, want) {
			t.Errorf("process actions %v lack %s", all, want)
		}
	}
	for _, env := range []string{"enqueue", "begin", "advance", "changeSource_a", "deliverCache"} {
		if slices.Contains(all, env) {
			t.Errorf("process actions %v include %s", all, env)
		}
	}
	less, err := ProcessActions(m, "error")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(less, "readSource_error") || !slices.Contains(less, "readSource_found") || len(less) != len(all)-3 {
		t.Errorf("except error: %v", less)
	}
}

func TestSteeringModule(t *testing.T) {
	m := steerModel(t)
	uniform, err := Steering{}.module(m)
	if err != nil || uniform != m.Quint() || (Steering{}).stepName() != "step" {
		t.Fatalf("uniform strategy changed the model: %v", err)
	}
	s, err := PreferProcess(m, 3, "error")
	if err != nil {
		t.Fatal(err)
	}
	src, err := s.module(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(src, strings.TrimSuffix(m.Quint(), "}\n")) || !strings.HasSuffix(src, "}\n") {
		t.Fatal("steered module does not extend the model")
	}
	line := src[strings.Index(src, " action "+steeredStep+" = any { "):]
	line = line[strings.Index(line, "{ ")+2 : strings.Index(line, " }")]
	listed := map[string]int{}
	for _, a := range strings.Split(line, ", ") {
		listed[a]++
	}
	for action, want := range map[string]int{"writeTarget_ok": 3, "node2_assign": 3, "writeTarget_error": 1, "enqueue": 1} {
		if listed[action] != want {
			t.Errorf("%s listed %d times; want %d", action, listed[action], want)
		}
	}
	if s.stepName() != steeredStep {
		t.Errorf("step %s", s.stepName())
	}
	for name, bad := range map[string]Steering{
		"unknown action": {Weights: map[string]int{"nope": 2}},
		"zero weight":    {Weights: map[string]int{"enqueue": 0}},
		"weight too big": {Weights: map[string]int{"enqueue": MaxWeight + 1}},
		"all equal":      {Weights: map[string]int{"enqueue": 1}},
	} {
		if _, err := bad.module(m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := PreferProcess(m, 1); err == nil {
		t.Error("weight 1 accepted: it is uniform sampling")
	}
}

func TestRecord(t *testing.T) {
	r := Options{Samples: 4, Steps: 9, Seed: 3}.Record()
	if r.Strategy != StrategyUniform || r.Weights != nil || r.Validate() != nil {
		t.Fatalf("uniform record %+v", r)
	}
	s := Steering{Weights: map[string]int{"a": 5}}
	r = Options{Samples: 4, Steps: 9, Seed: 3, Steering: s}.Record()
	s.Weights["a"] = 6
	if r.Strategy != StrategyWeighted || r.Weights["a"] != 5 || r.Samples != 4 || r.Steps != 9 || r.Seed != 3 || r.Validate() != nil {
		t.Fatalf("weighted record %+v", r)
	}
}

// TestSteeredSamplingReachesCompletion runs the real Quint CLI when
// QUINT_BIN is set. Uniform sampling of ConfigMirror's model with the
// evidence bounds reaches the successful write in 3 of 40 traces; steering
// towards process completion must reach it in at least half, and every
// steered trace must still be a trace of the model.
func TestSteeredSamplingReachesCompletion(t *testing.T) {
	exe := os.Getenv("QUINT_BIN")
	if exe == "" {
		t.Skip("QUINT_BIN unset")
	}
	m := steerModel(t)
	s, err := PreferProcess(m, 8, "error")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := Sample(ctx, m, Options{Executable: exe, Samples: 40, Steps: 60, Seed: 20261013, Steering: s})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := Differential(m, result.Traces)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reached["writeTarget_ok"] < 20 {
		t.Errorf("steered traces reach writeTarget_ok in %d of 40", stats.Reached["writeTarget_ok"])
	}
	// Failure outcomes stay reachable.
	for _, a := range []string{"readSource_error", "readTarget_error", "writeTarget_error"} {
		if stats.Reached[a] == 0 {
			t.Errorf("no steered trace takes %s", a)
		}
	}
	t.Logf("reached: %v", stats.Reached)
}

func TestDifferentialCountsReachedTraces(t *testing.T) {
	m := steerModel(t)
	init, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	next, err := m.Next(init, "enqueue")
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.Next(next, "enqueue")
	if err != nil {
		t.Fatal(err)
	}
	trace := conformance.Trace{Status: "ok", States: []conformance.State{{Event: "init", Values: init}, {Event: "enqueue", Values: next}, {Event: "enqueue", Values: again}}}
	stats, err := Differential(m, []conformance.Trace{trace, trace})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Coverage["enqueue"] != 4 || stats.Reached["enqueue"] != 2 || stats.Reached["init"] != 0 {
		t.Fatalf("coverage %v reached %v", stats.Coverage, stats.Reached)
	}
}

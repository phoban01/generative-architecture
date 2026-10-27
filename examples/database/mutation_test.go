package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/mutation"
)

var schedules = []string{"absent", "unsupported", "pending", "provisioned", "repeated"}

// TestMutationSuite runs mutations.yaml with the model and replay
// detectors. Every expected detector must fire, and the test records which
// detector caught each mutation: a controller change reaches only the
// model, a wiring change the model, the native handlers or both.
func TestMutationSuite(t *testing.T) {
	suite, err := mutation.Load("mutations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	load := func(overlay map[string][]byte) (Example, error) {
		mutated, err := loadOverlay(overlay)
		if err != nil {
			return Example{}, err
		}
		if mutated.Constrained.Quint() == base.Constrained.Quint() {
			return Example{}, fmt.Errorf("mutation leaves the model unchanged")
		}
		return mutated, nil
	}
	rebuilt := mutation.ModelDetector(func(overlay map[string][]byte) (*model.Compiled, []conformance.Trace, error) {
		mutated, err := load(overlay)
		if err != nil {
			return nil, nil, err
		}
		traces, err := scheduleTraces(mutated)
		return mutated.Constrained, traces, err
	})
	replay := mutation.Detector{Name: "replay", Detect: func(overlay map[string][]byte) ([]string, error) {
		mutated, err := load(overlay)
		if err != nil {
			return nil, err
		}
		return nativeDivergence(t, base, mutated), nil
	}}
	matrix, detections, err := mutation.RunDetectors(suite, rebuilt, replay)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("detection matrix:\n%s\ndetections:\n%s", matrix, detections)
	if escaped := suite.Escaped(matrix); len(escaped) != 0 {
		t.Fatalf("mutations escaped their detectors: %v\n%s", escaped, matrix)
	}
	want := map[string][]string{
		"contract-check":         {"model"},
		"provisioning-check":     {"model"},
		"wrong-endpoint":         {"model"},
		"missing-instance-watch": {"replay"},
		"wrong-mapping":          {"model"},
		"generation-predicate":   {"model", "replay"},
	}
	if got := suite.CaughtBy(detections); !reflect.DeepEqual(got, want) {
		t.Fatalf("caught by %v, want %v\n%s", got, want, detections)
	}
}

// loadOverlay generates the example from controller.go and wiring.go, each
// replaced by its overlay entry when it has one. The overlay is keyed by
// absolute path; a mutation of any other file has no detector.
func loadOverlay(overlay map[string][]byte) (Example, error) {
	sources := map[string][]byte{}
	used := 0
	for _, name := range []string{"controller.go", "wiring.go"} {
		path, err := filepath.Abs(name)
		if err != nil {
			return Example{}, err
		}
		if b, ok := overlay[path]; ok {
			sources[name] = b
			used++
			continue
		}
		if sources[name], err = os.ReadFile(name); err != nil {
			return Example{}, err
		}
	}
	if used != len(overlay) {
		return Example{}, fmt.Errorf("the overlay replaces a file other than controller.go and wiring.go")
	}
	return LoadSources(".", sources["controller.go"], sources["wiring.go"])
}

// scheduleTraces runs each schedule of the model up to the first step it
// cannot take.
func scheduleTraces(e Example) ([]conformance.Trace, error) {
	var out []conformance.Trace
	for _, name := range schedules {
		actions, _ := e.scheduleActions(name)
		trace, err := conformance.Scenario(e.Model, actions...)
		if err != nil {
			return nil, fmt.Errorf("schedule %s: %w", name, err)
		}
		out = append(out, trace)
	}
	return out, nil
}

// nativeDivergence replays each schedule of the unchanged model through the
// watches of the mutated wiring. It names the observed variables that
// differ at the first step where the native handlers disagree.
func nativeDivergence(t *testing.T, base, mutated Example) []string {
	t.Helper()
	native := base
	native.Watches = mutated.Watches
	var out []string
	for _, name := range schedules {
		trace, err := base.Schedule(name)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, divergence(t, native, trace)...)
	}
	return out
}

func divergence(t *testing.T, e Example, trace conformance.Trace) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a := New(ctx, e, false)
	defer a.Close()
	for i, s := range trace.States {
		if i > 0 {
			if err := a.Apply(ctx, s.Event); err != nil {
				t.Fatalf("step %d (%s): %v", i, s.Event, err)
			}
		}
		actual, err := a.Project(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var differ []string
		for _, name := range e.Observed {
			if !reflect.DeepEqual(actual[name], s.Values[name]) {
				differ = append(differ, name)
			}
		}
		if len(differ) > 0 {
			return differ
		}
	}
	return nil
}

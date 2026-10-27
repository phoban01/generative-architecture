package database

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/mutation"
)

var schedules = []string{"absent", "unsupported", "pending", "provisioned", "repeated"}

// TestMutationSuite runs mutations.yaml. Every expected detector must fire.
func TestMutationSuite(t *testing.T) {
	suite, err := mutation.Load("mutations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	var mutated Example
	build := func(files map[string][]byte) error {
		sources := map[string][]byte{}
		for _, name := range []string{"controller.go", "wiring.go"} {
			if sources[name], err = os.ReadFile(name); err != nil {
				return err
			}
		}
		for name, b := range files {
			if _, ok := sources[name]; !ok {
				return fmt.Errorf("mutation of %s has no detector", name)
			}
			sources[name] = b
		}
		mutated, err = LoadSources(".", sources["controller.go"], sources["wiring.go"])
		if err != nil {
			return err
		}
		if mutated.Constrained.Quint() == base.Constrained.Quint() {
			return fmt.Errorf("mutation leaves the model unchanged")
		}
		return nil
	}
	check := func() []string {
		detected := map[string]bool{}
		for _, c := range violatedConstraints(t, mutated) {
			detected[c] = true
		}
		for _, v := range nativeDivergence(t, base, mutated) {
			detected[v] = true
		}
		var out []string
		for name := range detected {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}
	matrix, err := mutation.Run(suite, build, check)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("detection matrix:\n%s", matrix)
	if escaped := suite.Escaped(matrix); len(escaped) != 0 {
		t.Fatalf("mutations escaped their detectors: %v\n%s", escaped, matrix)
	}
}

// violatedConstraints runs each schedule of the mutated model up to the
// first step it cannot take, and names the constraints its states violate.
func violatedConstraints(t *testing.T, e Example) []string {
	t.Helper()
	var out []string
	for _, name := range schedules {
		actions, _ := e.scheduleActions(name)
		trace, err := conformance.Scenario(e.Model, actions...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, s := range trace.States {
			out = append(out, violated(t, e.Constrained, s)...)
		}
	}
	return out
}

// violated names every constraint that state s violates.
func violated(t *testing.T, m *model.Compiled, s conformance.State) []string {
	t.Helper()
	err := m.Check(s.Values, s.Event)
	if err == nil {
		return nil
	}
	name, ok := strings.CutPrefix(err.Error(), "property ")
	name, found := strings.CutSuffix(name, " violated")
	if !ok || !found {
		t.Fatalf("constraint check failed: %v", err)
	}
	return []string{name}
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

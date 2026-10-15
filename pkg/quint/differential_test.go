package quint_test

import (
	"context"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/quint"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureModel compiles the committed example TransitionModel; its traces
// are read-only evidence produced by an earlier Quint run.
func fixtureModel(t *testing.T) *model.Compiled {
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

func fixtureTraces(t *testing.T) []conformance.Trace {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "configmirror", "traces", "*.itf.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no trace fixtures")
	}
	var traces []conformance.Trace
	for _, p := range paths {
		trace, err := conformance.ReadITF(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		traces = append(traces, trace)
	}
	return traces
}

func requireToolFailure(t *testing.T, err error, step int, event, message string) {
	t.Helper()
	f, ok := conformance.AsFailure(err)
	if !ok {
		t.Fatalf("expected *conformance.Failure, got %T: %v", err, err)
	}
	if f.Category != conformance.CategoryTool || f.Step != step || f.Event != event || !strings.Contains(err.Error(), message) {
		t.Fatalf("got %s at step %d (%s): %v; want tool at step %d (%s) mentioning %q", f.Category, f.Step, f.Event, err, step, event, message)
	}
}

func TestDifferentialCountsFixtureTransitions(t *testing.T) {
	m := fixtureModel(t)
	traces := fixtureTraces(t)
	stats, err := quint.Differential(m, traces)
	if err != nil {
		t.Fatal(err)
	}
	transitions := 0
	for _, trace := range traces {
		transitions += len(trace.States) - 1
	}
	if stats.Traces != len(traces) || stats.Transitions != transitions || stats.Checks != transitions+len(traces) {
		t.Fatalf("%+v over %d traces with %d transitions", stats, len(traces), transitions)
	}
	if stats.Transitions == 0 || stats.Coverage["writeTarget_ok"] == 0 || stats.Coverage["changeSource_b"] == 0 {
		t.Fatalf("coverage %v", stats.Coverage)
	}
	total := 0
	for _, n := range stats.Coverage {
		total += n
	}
	if total != stats.Transitions {
		t.Fatalf("coverage sums to %d, not %d", total, stats.Transitions)
	}
	// The committed report counts the same evidence.
	var report map[string]any
	if err := krm.ReadResult(filepath.Join("..", "..", "examples", "configmirror", "report.yaml"), "VerificationReport", &report); err != nil {
		t.Fatal(err)
	}
	if report["traces"] != stats.Traces || report["transitions"] != stats.Transitions {
		t.Fatalf("report counts %v traces and %v transitions, differential %+v", report["traces"], report["transitions"], stats)
	}
}

func TestDifferentialReportsFirstMismatchAsToolFailure(t *testing.T) {
	m := fixtureModel(t)
	traces := fixtureTraces(t)[:3]
	clone := func(trace conformance.Trace) conformance.Trace {
		out := conformance.Trace{Status: trace.Status, States: make([]conformance.State, len(trace.States))}
		for i, s := range trace.States {
			values := map[string]any{}
			for k, v := range s.Values {
				values[k] = v
			}
			out.States[i] = conformance.State{Event: s.Event, Values: values}
		}
		return out
	}
	t.Run("value drift", func(t *testing.T) {
		altered := []conformance.Trace{traces[0], clone(traces[1]), traces[2]}
		altered[1].States[5].Values["now"] = int64(99)
		event := altered[1].States[5].Event
		stats, err := quint.Differential(m, altered)
		requireToolFailure(t, err, 5, event, "trace 1 step 5 ("+event+"): CEL/trace mismatch")
		if stats.Traces != 1 || stats.Transitions != len(traces[0].States)-1+5 {
			t.Fatalf("%+v", stats)
		}
	})
	t.Run("disabled transition", func(t *testing.T) {
		altered := []conformance.Trace{clone(traces[0])}
		altered[0].States[1].Event = "writeTarget_ok"
		_, err := quint.Differential(m, altered)
		requireToolFailure(t, err, 1, "writeTarget_ok", "CEL rejects the Quint transition")
	})
	t.Run("unknown action", func(t *testing.T) {
		altered := []conformance.Trace{clone(traces[0])}
		altered[0].States[2].Event = "nothing"
		_, err := quint.Differential(m, altered)
		requireToolFailure(t, err, 2, "nothing", "unknown action nothing")
	})
	t.Run("status", func(t *testing.T) {
		altered := []conformance.Trace{traces[0], {Status: "violation", States: traces[1].States}}
		_, err := quint.Differential(m, altered)
		requireToolFailure(t, err, 0, "", "trace 1: expected an ok trace")
	})
	t.Run("empty", func(t *testing.T) {
		_, err := quint.Differential(m, nil)
		requireToolFailure(t, err, 0, "", "no traces")
		_, err = quint.Differential(nil, traces)
		requireToolFailure(t, err, 0, "", "compiled model required")
	})
	t.Run("property", func(t *testing.T) {
		// A model whose CEL properties are stricter than the lowering Quint
		// sampled: the first state that breaks them is a translation defect.
		var spec model.Spec
		if _, err := krm.Read(filepath.Join("..", "..", "examples", "configmirror", "generated", "model.yaml"), "TransitionModel", &spec); err != nil {
			t.Fatal(err)
		}
		spec.Properties = append(spec.Properties, model.Property{Name: "neverAdvances", Expression: "now == 0"})
		strict, err := model.Compile(spec)
		if err != nil {
			t.Fatal(err)
		}
		_, err = quint.Differential(strict, traces)
		f, ok := conformance.AsFailure(err)
		if !ok || f.Category != conformance.CategoryTool || f.Event != "advance" || !strings.Contains(err.Error(), "property neverAdvances violated") {
			t.Fatal(err)
		}
	})
}

// TestDifferentialOverSampledTraces runs the real Quint CLI when QUINT_BIN
// names one, so the check is exercised end to end on fresh traces.
func TestDifferentialOverSampledTraces(t *testing.T) {
	exe := os.Getenv("QUINT_BIN")
	if exe == "" {
		t.Skip("QUINT_BIN unset")
	}
	if _, err := os.Stat(exe); err != nil {
		t.Skipf("QUINT_BIN: %v", err)
	}
	m := fixtureModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sampled, err := quint.Sample(ctx, m, quint.Options{Executable: exe, Samples: 3, Steps: 8, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := quint.Differential(m, sampled.Traces)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Traces != 3 || stats.Transitions != 24 || stats.Checks != 27 {
		t.Fatalf("%+v", stats)
	}
}

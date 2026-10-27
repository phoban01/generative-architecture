package mutation

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/model"
)

// counterModel builds a one-action model whose increment is read from the
// counter program text, so a mutated text rebuilds a different model.
func counterModel(program string) (*model.Compiled, []conformance.Trace, error) {
	step := "0"
	switch {
	case strings.Contains(program, "count = count + 2"):
		step = "2"
	case strings.Contains(program, "count = count + 1"):
		step = "1"
	}
	m, err := model.Compile(model.Spec{
		Name:    "Counter",
		State:   map[string]model.Variable{"count": {Type: "int", Initial: "0"}},
		Actions: []model.Action{{Name: "tick", Guard: "true", Updates: map[string]expr.Expression{"count": expr.Expression("count + " + step)}}},
		Properties: []model.Property{
			{Name: "bounded", Expression: "count <= 1"},
			{Name: "progress", Expression: `event != "tick" || count > 0`},
		},
	})
	if err != nil {
		return nil, nil, err
	}
	t, err := conformance.Scenario(m, "tick")
	if err != nil {
		return nil, nil, err
	}
	return m, []conformance.Trace{t}, nil
}

func TestRunDetectorsRecordsWhichDetectorCaughtEachMutation(t *testing.T) {
	s, err := Load(writeSuite(t, suiteDoc))
	if err != nil {
		t.Fatal(err)
	}
	file, err := filepath.Abs(filepath.Join(s.Root, "counter.txt"))
	if err != nil {
		t.Fatal(err)
	}
	contents := func(overlay map[string][]byte) (string, error) {
		b, ok := overlay[file]
		if !ok || len(overlay) != 1 {
			return "", fmt.Errorf("overlay is not keyed by the absolute path: %v", overlay)
		}
		return string(b), nil
	}
	rebuilt := ModelDetector(func(overlay map[string][]byte) (*model.Compiled, []conformance.Trace, error) {
		program, err := contents(overlay)
		if err != nil {
			return nil, nil, err
		}
		return counterModel(program)
	})
	// The stand-in replay sees only a skipped increment.
	replay := Detector{Name: "replay", Detect: func(overlay map[string][]byte) ([]string, error) {
		program, err := contents(overlay)
		if err != nil {
			return nil, err
		}
		if v := evaluate(program); len(v) == 1 && v[0] == "progress" {
			return v, nil
		}
		return nil, nil
	}}
	matrix, detections, err := RunDetectors(s, rebuilt, replay)
	if err != nil {
		t.Fatal(err)
	}
	if want := "double-increment: +bounded -progress\nskip-increment: +progress\n"; matrix.String() != want {
		t.Fatalf("matrix:\n%s\nwant:\n%s", matrix, want)
	}
	if want := "double-increment: bounded[model]\nskip-increment: progress[model,replay]\n"; detections.String() != want {
		t.Fatalf("detections:\n%s\nwant:\n%s", detections, want)
	}
	if got, want := s.CaughtBy(detections), map[string][]string{"double-increment": {"model"}, "skip-increment": {"model", "replay"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("caught by %v, want %v", got, want)
	}

	failing := Detector{Name: "replay", Detect: func(map[string][]byte) ([]string, error) { return nil, fmt.Errorf("build failed") }}
	if _, _, err = RunDetectors(s, rebuilt, failing); err == nil || !strings.Contains(err.Error(), "skip-increment: detector replay: build failed") {
		t.Fatalf("detector failure not located: %v", err)
	}
	empty := ModelDetector(func(map[string][]byte) (*model.Compiled, []conformance.Trace, error) {
		m, _, err := counterModel("")
		return m, nil, err
	})
	if _, _, err = RunDetectors(s, empty); err == nil || !strings.Contains(err.Error(), "no traces") {
		t.Fatalf("vacuous model detector accepted: %v", err)
	}
	if _, _, err = RunDetectors(s, replay, replay); err == nil || !strings.Contains(err.Error(), "unique") {
		t.Fatalf("duplicate detector accepted: %v", err)
	}
	if _, _, err = RunDetectors(s); err == nil {
		t.Fatal("run without detectors accepted")
	}
}

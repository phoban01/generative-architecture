package configmirror

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/mutation"
)

// committedModel loads the generated model the evidence was produced from.
func committedModel(t *testing.T) (model.Spec, *model.Compiled) {
	t.Helper()
	var spec model.Spec
	if _, err := krm.Read(SpecFile, "TransitionModel", &spec); err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	return spec, m
}

func TestScenarios(t *testing.T) {
	spec, m := committedModel(t)
	for name, events := range Scenarios {
		t.Run(name, func(t *testing.T) {
			trace, err := Scenario(spec, events...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Replay(context.Background(), m, trace); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQuintTraces(t *testing.T) {
	_, m := committedModel(t)
	paths, err := filepath.Glob(TraceGlob)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 40 {
		t.Fatalf("expected 40 recorded Quint traces; run make library-demo; got %d", len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			trace, err := conformance.ReadITF(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Replay(context.Background(), m, trace); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The adapter observes the abstraction's object state, the cache's view of
// the cached source and the runtime queue; the process locals (program
// counter, snapshots, errors, versions) stay model-internal.
func TestObservedVariables(t *testing.T) {
	_, m := committedModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := New(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	want := []string{
		"ConfigMap_copy_data_value", "ConfigMap_copy_exists", "ConfigMap_source_data_value", "ConfigMap_source_exists",
		"cached_ConfigMap_source_data_value", "dirty", "due", "now", "processing", "ready", "retries",
	}
	if got := a.Observed(); !reflect.DeepEqual(got, want) {
		t.Fatalf("observed %v, want %v", got, want)
	}
}

// Without mutations the committed evidence replays cleanly, so whatever a
// mutated build reports is the mutation's doing.
func TestUnmutatedDetectsNothing(t *testing.T) {
	detected, err := Detect(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(detected) != 0 {
		t.Fatalf("unmutated build detected %v", detected)
	}
}

// The mutation suite regenerates the model from each mutant and compiles
// each mutant with the go command to replay the committed evidence through
// it. Every expectation must be met, and the test records which detector
// caught each mutation: the adapter change leaves the model unchanged, so
// only replay sees it.
func TestMutationsDetected(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles one mutant per mutation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	suite, matrix, detections, err := RunMutationDetectors(ctx, filepath.Join("..", ".."), ".")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("detections:\n%s", detections)
	if got, want := suite.CaughtBy(detections), map[string][]string{"direct-source": {"replay"}, "wrong-write": {"model", "replay"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("caught by %v, want %v\n%s", got, want, detections)
	}
	var names []string
	for _, m := range suite.Spec.Mutations {
		names = append(names, m.Name)
	}
	if !reflect.DeepEqual(names, []string{"direct-source", "wrong-write"}) {
		t.Fatalf("mutations %v", names)
	}
	if escaped := suite.Escaped(matrix); len(escaped) != 0 {
		t.Fatalf("escaped mutations %v; matrix:\n%s", escaped, matrix)
	}
	t.Logf("detection matrix:\n%s", matrix)
}

// The model detector alone needs no build: the wrong-write mutant,
// regenerated from overlaid source, violates writesObservedValue, and the
// adapter change leaves the model and its properties untouched.
func TestRebuiltModelDetectsWrongWrite(t *testing.T) {
	suite, err := mutation.Load(MutationsFile)
	if err != nil {
		t.Fatal(err)
	}
	_, detections, err := mutation.RunDetectors(suite, ModelDetector("."))
	if err != nil {
		t.Fatal(err)
	}
	if got := detections["wrong-write"]["writesObservedValue"]; !reflect.DeepEqual(got, []string{"model"}) {
		t.Fatalf("wrong-write detections:\n%s", detections)
	}
	if len(detections["direct-source"]) != 0 {
		t.Fatalf("the adapter change altered the model:\n%s", detections)
	}
}

func TestCancelAtEveryBoundary(t *testing.T) {
	_, m := committedModel(t)
	for _, steps := range [][]string{{"enqueue", "begin"}, {"enqueue", "begin", "readSource_found"}, {"enqueue", "begin", "readSource_found", "readTarget_found"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		a, err := New(ctx, m)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		for _, step := range steps {
			if err = a.Apply(ctx, step); err != nil {
				a.Close()
				cancel()
				t.Fatal(err)
			}
		}
		err = a.Close()
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
}

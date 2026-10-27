package configmirror

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
)

func generate(t *testing.T) (model.Spec, *model.Compiled) {
	t.Helper()
	spec, err := Generate(".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	return spec, m
}

func TestGenerateModel(t *testing.T) {
	spec, m := generate(t)
	abs, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// State is the abstraction's variables, the runtime variables and the
	// process control.
	for _, name := range append(abs.SortedVariables(), "pc", "processing", "ready", "dirty", "retries", "now", "due", "finished", "failed") {
		if _, ok := spec.State[name]; !ok {
			t.Errorf("state lacks %s", name)
		}
	}
	// The effects are the reconciler's three client calls, each with the
	// operation and consistency of the client it goes through.
	configMap := effect.Resource{Version: "v1", Kind: "ConfigMap"}
	wantEffects := []effect.Descriptor{
		{ID: "readSource", Resource: configMap, Operation: "get", Consistency: "cached"},
		{ID: "readTarget", Resource: configMap, Operation: "get", Consistency: "direct"},
		{ID: "writeTarget", Resource: configMap, Operation: "update", Consistency: "direct"},
	}
	if !reflect.DeepEqual(spec.Effects, wantEffects) {
		t.Errorf("effects %+v, want %+v", spec.Effects, wantEffects)
	}
	// The committed model is the evidence source: make library-demo
	// writes it in both forms and samples the traces from it.
	committed, err := os.ReadFile(ModelFile)
	if err != nil {
		t.Fatalf("%v; run make library-demo", err)
	}
	if string(committed) != m.Quint() {
		t.Fatalf("%s is stale; run make library-demo", ModelFile)
	}
	var saved model.Spec
	if _, err := krm.Read(SpecFile, "TransitionModel", &saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, spec) {
		t.Fatalf("%s is stale; run make library-demo", SpecFile)
	}
	quint := os.Getenv("QUINT_BIN")
	if quint == "" {
		t.Skip("QUINT_BIN not set; skipping Quint typecheck of the generated model")
	}
	if out, err := exec.Command(quint, "typecheck", ModelFile).CombinedOutput(); err != nil {
		t.Fatalf("quint typecheck %s: %v\n%s", ModelFile, err, out)
	}
}

// Seeded random walks of the generated model: as many and as long as the
// Quint traces of the evidence. They evaluate the original CEL, not the
// Quint lowering, so they check the generated model independently of the
// traces on disk.
const (
	walks    = 40
	walkSize = 60
	walkSeed = 20261013
)

// walk takes seeded random walks of spec from its initial state. The
// process's internal steps (branches, assignments, returns) are
// deterministic and taken as soon as one is enabled; otherwise each step is
// an action drawn uniformly from the enabled ones. walk checks the
// constraints and runtime properties on every successor. It returns the
// number of steps taken per action and the violations found.
func walk(t *testing.T, spec model.Spec) (map[string]int, []string) {
	t.Helper()
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	eval, err := expr.NewEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(walkSeed, 0))
	taken := map[string]int{}
	var violations []string
	for n := range walks {
		state, err := m.Initial()
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Check(state, "init"); err != nil {
			violations = append(violations, fmt.Sprintf("walk %d initial state: %v", n, err))
		}
		for i := range walkSize {
			var enabled, internal []string
			for _, a := range spec.Actions {
				v, err := eval.Eval(a.Guard, state)
				if err != nil {
					t.Fatalf("guard of %s: %v", a.Name, err)
				}
				switch {
				case v != true:
				case strings.HasPrefix(a.Name, "node"):
					internal = append(internal, a.Name)
				default:
					enabled = append(enabled, a.Name)
				}
			}
			var event string
			switch {
			case len(internal) > 1:
				t.Fatalf("internal steps %v enabled together", internal)
			case len(internal) == 1:
				event = internal[0]
			case len(enabled) > 0:
				event = enabled[r.IntN(len(enabled))]
			}
			if event == "" {
				break
			}
			if state, err = m.Next(state, event); err != nil {
				t.Fatal(err)
			}
			taken[event]++
			if err := m.Check(state, event); err != nil {
				violations = append(violations, fmt.Sprintf("walk %d step %d (%s): %v", n, i+1, event, err))
			}
		}
	}
	return taken, violations
}

// The generated model satisfies the independent constraints and the
// runtime properties on every step of the walks, and the walks take every
// environment, runtime and effect-outcome action the abstraction makes
// reachable.
func TestGeneratedModelWalks(t *testing.T) {
	spec, _ := generate(t)
	taken, violations := walk(t, spec)
	for _, v := range violations {
		t.Error(v)
	}
	for _, a := range []string{
		"changeSource_a", "changeSource_b", "changeSource_empty", "deliverCache", "advance", "fire",
		"enqueue", "begin", "finish", "finishError",
		"readSource_found", "readSource_error", "readTarget_found", "readTarget_error", "writeTarget_ok", "writeTarget_error",
	} {
		if taken[a] == 0 {
			t.Errorf("not taken by the walks: %s", a)
		}
	}
	t.Logf("actions taken: %v", taken)
}

// The constraints are not vacuous for the generated model: a model whose
// write copies the live source instead of the value read, and one without
// the retry bound, violate the constraint written for each on the same
// walks.
func TestGeneratedModelMutationsViolateConstraints(t *testing.T) {
	for name, c := range map[string]struct {
		mutate func(*model.Action)
		want   string
	}{
		"wrong-write": {func(a *model.Action) {
			if a.Name == "writeTarget_ok" {
				a.Updates["ConfigMap_copy_data_value"] = "ConfigMap_source_data_value"
			}
		}, "property writesObservedValue violated"},
		"unbounded-retries": {func(a *model.Action) {
			if a.Name == "begin" {
				a.Guard = expr.Expression(strings.TrimSuffix(string(a.Guard), " && retries < 3"))
			}
		}, "property boundedRetries violated"},
	} {
		t.Run(name, func(t *testing.T) {
			spec, _ := generate(t)
			changed := false
			for i := range spec.Actions {
				before := fmt.Sprint(spec.Actions[i])
				c.mutate(&spec.Actions[i])
				changed = changed || fmt.Sprint(spec.Actions[i]) != before
			}
			if !changed {
				t.Fatalf("mutation %s changed no action", name)
			}
			_, violations := walk(t, spec)
			for _, v := range violations {
				if strings.Contains(v, c.want) {
					t.Logf("detected: %s", v)
					return
				}
			}
			t.Fatalf("mutation %s not detected (want %q in %d violations: %v)", name, c.want, len(violations), violations)
		})
	}
}

func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

package configmirror

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
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
	// The effects are the handwritten model's, derived from the program.
	var hand model.Spec
	if _, err := krm.Read("system.yaml", "TransitionModel", &hand); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.Effects, hand.Effects) {
		t.Errorf("effects %+v, want %+v", spec.Effects, hand.Effects)
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

// correspondence relates the handwritten model to the generated one.
// Handwritten variables map to generated variables, except pc, whose values
// are compared as phases (the effect the process performs next, idle or
// done) because the two models number their program counters differently.
// Each handwritten action maps to the generated actions that refine it.
var correspondence = struct {
	vars    map[string]string
	actions map[string][]string
}{
	vars: map[string]string{
		"source": "ConfigMap_source_data_value", "cached": "cached_ConfigMap_source_data_value",
		"target": "ConfigMap_copy_data_value", "snapshot": "snapshot", "failed": "failed",
		"ready": "ready", "dirty": "dirty", "processing": "processing", "retries": "retries", "now": "now", "due": "due",
	},
	actions: map[string][]string{
		"changeSource":    {"changeSource_a", "changeSource_b", "changeSource_empty"},
		"deliverCache":    {"deliverCache"},
		"enqueue":         {"enqueue"},
		"begin":           {"begin"},
		"readSource":      {"readSource_found"},
		"readSourceError": {"readSource_notFound", "readSource_error"},
		"readTarget":      {"readTarget_found"},
		"readTargetError": {"readTarget_notFound", "readTarget_error"},
		"commit":          {"writeTarget_ok"},
		"writeError":      {"writeTarget_conflict", "writeTarget_notFound", "writeTarget_error"},
		"finish":          {"finish", "finishError"},
		"advance":         {"advance"},
		"fire":            {"fire"},
	},
}

// parity replays traces of the handwritten model against the generated one
// and collects every difference.
type parity struct {
	hand, gen         *model.Compiled
	handSpec, genSpec model.Spec
	eval              *expr.Evaluator
	handPhase         map[int64]string
	genPhase          map[int64]string
	internal          []string
	// differences are failures of parity; extras are generated behaviours
	// the handwritten model does not have.
	differences []string
	extras      map[string]int
	coverage    map[string]int
}

func (p *parity) guard(spec model.Spec, action string, state map[string]any) bool {
	for _, a := range spec.Actions {
		if a.Name == action {
			v, err := p.eval.Eval(a.Guard, state)
			if err != nil {
				p.differences = append(p.differences, fmt.Sprintf("guard of %s does not evaluate: %v", action, err))
				return false
			}
			return v == true
		}
	}
	p.differences = append(p.differences, "no action "+action)
	return false
}

func (p *parity) phaseOfHand(h map[string]any) string {
	pc := h["pc"].(int64)
	if pc == 0 {
		return "idle"
	}
	if e, ok := p.handPhase[pc]; ok {
		return e
	}
	return "done"
}

func (p *parity) phaseOfGen(g map[string]any) string {
	if g["finished"] == true {
		if g["processing"] == true {
			return "done"
		}
		return "idle"
	}
	pc := g["pc"].(int64)
	if e, ok := p.genPhase[pc]; ok {
		return e
	}
	return fmt.Sprintf("node%d", pc)
}

// compare lists the corresponding variables on which h and g disagree.
func (p *parity) compare(h, g map[string]any) []string {
	var out []string
	for _, hv := range sortedStrings(correspondence.vars) {
		gv := correspondence.vars[hv]
		if !reflect.DeepEqual(h[hv], g[gv]) {
			out = append(out, fmt.Sprintf("%s=%v but %s=%v", hv, h[hv], gv, g[gv]))
		}
	}
	if hp, gp := p.phaseOfHand(h), p.phaseOfGen(g); hp != gp {
		out = append(out, fmt.Sprintf("phase %s (pc=%v) but %s (pc=%v finished=%v)", hp, h["pc"], gp, g["pc"], g["finished"]))
	}
	return out
}

// settle takes the generated process's internal steps (branches,
// assignments, returns), which the handwritten model folds into its
// effect actions. At most one is enabled at a time.
func (p *parity) settle(g map[string]any) (map[string]any, error) {
	for range 100 {
		var enabled []string
		for _, a := range p.internal {
			if p.guard(p.genSpec, a, g) {
				enabled = append(enabled, a)
			}
		}
		if len(enabled) == 0 {
			return g, nil
		}
		if len(enabled) > 1 {
			return nil, fmt.Errorf("internal steps %v enabled together", enabled)
		}
		next, err := p.gen.Next(g, enabled[0])
		if err != nil {
			return nil, err
		}
		g = next
	}
	return nil, fmt.Errorf("internal steps do not terminate")
}

// step checks every handwritten action at (h, g) and returns the generated
// successor matching the trace's next state.
func (p *parity) step(where string, h, g map[string]any, event string, next map[string]any) (map[string]any, string) {
	var successor map[string]any
	var by string
	for _, a := range p.handSpec.Actions {
		handEnabled := p.guard(p.handSpec, a.Name, h)
		var enabled []string
		for _, c := range correspondence.actions[a.Name] {
			if p.guard(p.genSpec, c, g) {
				enabled = append(enabled, c)
			}
		}
		if handEnabled != (len(enabled) > 0) {
			p.differences = append(p.differences, fmt.Sprintf("%s: guard of %s is %v but %v enables %v", where, a.Name, handEnabled, correspondence.actions[a.Name], enabled))
			continue
		}
		if !handEnabled {
			continue
		}
		p.coverage[a.Name]++
		h2, err := p.hand.Next(h, a.Name)
		if err != nil {
			p.differences = append(p.differences, fmt.Sprintf("%s: %v", where, err))
			continue
		}
		matched := false
		var mismatches []string
		for _, c := range enabled {
			g2, err := p.gen.Next(g, c)
			if err == nil {
				g2, err = p.settle(g2)
			}
			if err != nil {
				p.differences = append(p.differences, fmt.Sprintf("%s: %s: %v", where, c, err))
				continue
			}
			if diff := p.compare(h2, g2); len(diff) > 0 {
				p.extras[a.Name+" -> "+c]++
				mismatches = append(mismatches, c+": "+strings.Join(diff, ", "))
				continue
			}
			matched = true
			if a.Name == event {
				successor, by = g2, c
			}
		}
		if !matched {
			p.differences = append(p.differences, fmt.Sprintf("%s: updates of %s have no generated counterpart: %s", where, a.Name, strings.Join(mismatches, "; ")))
		}
	}
	if successor != nil {
		if diff := p.compare(next, successor); len(diff) > 0 {
			p.differences = append(p.differences, fmt.Sprintf("%s: walk state after %s differs: %s", where, event, strings.Join(diff, ", ")))
		}
	}
	return successor, by
}

func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Parity walks: seeded random walks of the handwritten model, as many and
// as long as the Quint traces the handwritten model's evidence had.
const (
	parityWalks = 40
	paritySteps = 60
	paritySeed  = 20261013
)

// handwrittenWalks samples traces of the handwritten model with original
// CEL: from the initial state, each step takes an action drawn uniformly
// from the enabled ones with a fixed seed. The handwritten model's own
// Quint traces are no longer evidence; the walks keep the parity check
// independent of them.
func handwrittenWalks(t *testing.T, p *parity, walks, steps int) []conformance.Trace {
	t.Helper()
	r := rand.New(rand.NewPCG(paritySeed, 0))
	var out []conformance.Trace
	for range walks {
		state, err := p.hand.Initial()
		if err != nil {
			t.Fatal(err)
		}
		trace := conformance.Trace{Status: "ok", States: []conformance.State{{Event: "init", Values: state}}}
		for range steps {
			var enabled []string
			for _, a := range p.handSpec.Actions {
				if p.guard(p.handSpec, a.Name, state) {
					enabled = append(enabled, a.Name)
				}
			}
			if len(enabled) == 0 {
				break
			}
			event := enabled[r.IntN(len(enabled))]
			if state, err = p.hand.Next(state, event); err != nil {
				t.Fatal(err)
			}
			trace.States = append(trace.States, conformance.State{Event: event, Values: state})
		}
		out = append(out, trace)
	}
	return out
}

// checkParity replays seeded walks of the handwritten model against
// genSpec. It returns the comparison, the generated variables without a
// handwritten counterpart and the number of trace states checked.
func checkParity(t *testing.T, genSpec model.Spec) (*parity, []string, int) {
	t.Helper()
	gen, err := model.Compile(genSpec)
	if err != nil {
		t.Fatal(err)
	}
	var handSpec model.Spec
	if _, err := krm.Read("system.yaml", "TransitionModel", &handSpec); err != nil {
		t.Fatal(err)
	}
	hand, err := model.Compile(handSpec)
	if err != nil {
		t.Fatal(err)
	}
	eval, err := expr.NewEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	p := &parity{hand: hand, gen: gen, handSpec: handSpec, genSpec: genSpec, eval: eval, handPhase: map[int64]string{}, genPhase: map[int64]string{}, extras: map[string]int{}, coverage: map[string]int{}}

	// Phases: the handwritten pc at which each effect action is enabled,
	// and the generated node performing each effect.
	pcGuard := regexp.MustCompile(`^pc == (\d+)$`)
	for _, a := range handSpec.Actions {
		if m := pcGuard.FindStringSubmatch(string(a.Guard)); m != nil && a.Effect != "" {
			pc, _ := strconv.ParseInt(m[1], 10, 64)
			p.handPhase[pc] = a.Effect
		}
	}
	var program goflow.Program
	if err := krm.ReadResult(filepath.Join("generated", "process.yaml"), "ExtractedProcess", &program); err != nil {
		t.Fatal(err)
	}
	for _, n := range program.Nodes {
		if n.Effect != "" {
			p.genPhase[int64(n.ID)] = n.Effect
		}
	}

	// Static differences: every handwritten action has counterparts that
	// exist; generated actions and variables without a handwritten
	// counterpart are listed.
	genActions := map[string]bool{}
	for _, a := range genSpec.Actions {
		genActions[a.Name] = true
	}
	refined := map[string]bool{}
	for _, a := range handSpec.Actions {
		cs, ok := correspondence.actions[a.Name]
		if !ok || len(cs) == 0 {
			t.Errorf("handwritten action %s has no counterpart", a.Name)
		}
		for _, c := range cs {
			if !genActions[c] {
				t.Errorf("counterpart %s of %s is not a generated action", c, a.Name)
			}
			refined[c] = true
		}
	}
	for _, a := range genSpec.Actions {
		if refined[a.Name] {
			continue
		}
		if !strings.HasPrefix(a.Name, "node") {
			t.Errorf("generated action %s refines no handwritten action and is not an internal process step", a.Name)
			continue
		}
		p.internal = append(p.internal, a.Name)
	}
	mapped := map[string]bool{"pc": true, "finished": true}
	for _, gv := range correspondence.vars {
		mapped[gv] = true
	}
	var hidden []string
	for _, name := range sortedStrings(genSpec.State) {
		if !mapped[name] {
			hidden = append(hidden, name)
		}
	}
	for hv := range handSpec.State {
		if _, ok := correspondence.vars[hv]; !ok && hv != "pc" {
			t.Errorf("handwritten variable %s has no counterpart", hv)
		}
	}

	states := 0
	for n, trace := range handwrittenWalks(t, p, parityWalks, paritySteps) {
		path := fmt.Sprintf("walk-%03d", n)
		g, err := gen.Initial()
		if err != nil {
			t.Fatal(err)
		}
		if diff := p.compare(trace.States[0].Values, g); len(diff) > 0 {
			p.differences = append(p.differences, fmt.Sprintf("%s initial state: %s", filepath.Base(path), strings.Join(diff, ", ")))
		}
		for i := 0; i+1 < len(trace.States); i++ {
			where := fmt.Sprintf("%s step %d", filepath.Base(path), i+1)
			event := trace.States[i+1].Event
			states++
			next, by := p.step(where, trace.States[i].Values, g, event, trace.States[i+1].Values)
			if next == nil {
				p.differences = append(p.differences, where+": no generated counterpart of "+event+"; stopping this trace")
				break
			}
			// The constraints and runtime properties hold on the
			// generated state, with the generated event.
			if err := gen.Check(next, by); err != nil {
				p.differences = append(p.differences, fmt.Sprintf("%s (%s as %s): %v", where, event, by, err))
			}
			g = next
		}
	}
	return p, hidden, states
}

func TestParityWithHandwrittenModel(t *testing.T) {
	spec, _ := generate(t)
	p, hidden, states := checkParity(t, spec)
	t.Logf("checked every handwritten action on %d walk states; constraints and runtime properties evaluated on each generated successor", states)
	t.Logf("handwritten actions exercised (enabled states): %v", p.coverage)
	t.Logf("generated-only internal process steps: %v", p.internal)
	t.Logf("generated-only variables (no handwritten counterpart): %v", hidden)
	for _, k := range sortedStrings(p.extras) {
		t.Logf("generated-only behaviour: %s enabled on %d trace states with a successor the handwritten model does not reach", k, p.extras[k])
	}
	for _, a := range p.handSpec.Actions {
		if p.coverage[a.Name] == 0 {
			t.Errorf("not exercised by the walks: %s", a.Name)
		}
	}
	if len(p.differences) > 0 {
		for _, d := range p.differences {
			t.Log("difference: " + d)
		}
		t.Fatalf("%d parity differences", len(p.differences))
	}
}

// The parity check is not vacuous: a generated model whose write copies the
// live source instead of the snapshot, and one without the retry bound, are
// reported with the handwritten actions they break.
func TestParityReportsDifferences(t *testing.T) {
	for name, mutate := range map[string]func(*model.Action){
		"wrong-write": func(a *model.Action) {
			if a.Name == "writeTarget_ok" {
				a.Updates["ConfigMap_copy_data_value"] = "ConfigMap_source_data_value"
			}
		},
		"unbounded-retries": func(a *model.Action) {
			if a.Name == "begin" {
				a.Guard = expr.Expression(strings.TrimSuffix(string(a.Guard), " && retries < 3"))
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec, _ := generate(t)
			for i := range spec.Actions {
				mutate(&spec.Actions[i])
			}
			p, _, _ := checkParity(t, spec)
			want := map[string]string{"wrong-write": "commit", "unbounded-retries": "guard of begin"}[name]
			found := false
			for _, d := range p.differences {
				found = found || strings.Contains(d, want)
			}
			if !found {
				t.Fatalf("mutation %s not reported (want %q in %d differences: %v)", name, want, len(p.differences), p.differences)
			}
			t.Logf("%s: %d differences, first: %s", name, len(p.differences), p.differences[0])
		})
	}
}

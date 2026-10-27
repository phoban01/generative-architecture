package configmirror

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/likec4"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/mutation"
)

// Evidence files, relative to the example directory. cmd/library-demo
// writes them; the tests replay and check them without Quint.
const (
	// ModelFile is the generated model in Quint, which Quint samples.
	ModelFile = "generated/Model.qnt"
	// SpecFile is the same generated model as a TransitionModel document,
	// the canonical form replay and the CEL checks read (ADR-0001).
	SpecFile = "generated/model.yaml"
	// DiagramFile is the LikeC4 view of the generated model.
	DiagramFile = "generated/architecture.c4"
	// TraceGlob matches the sampled Quint traces.
	TraceGlob     = "traces/*.itf.json"
	ReportFile    = "report.yaml"
	ReceiptFile   = "receipt.yaml"
	MutationsFile = "mutations.yaml"
)

// Diagram draws the effects of the generated model with pkg/likec4: the
// resource view of one controller and the API effects extracted from
// controller.go through the built-in client summaries. It is the content
// of DiagramFile.
func Diagram(spec model.Spec) (string, error) {
	const controller = "configmirror"
	doc := ir.Document{
		Controllers: []ir.Controller{{ID: controller, Name: spec.Name, Provenance: ir.Provenance{Source: ir.Source{File: "controller.go"}}}},
		Effects:     likec4.EffectsOf(controller, spec.Effects, ir.Extracted, ir.Source{File: "controller.go"}),
	}
	elements, relations, view, err := likec4.ResourceView("config_mirror_resources", doc)
	if err != nil {
		return "", err
	}
	view.Title = spec.Name + ": API effects"
	var w likec4.Workspace
	if err := w.Add(elements, relations, view); err != nil {
		return "", err
	}
	src, err := w.Source()
	return string(src), err
}

// Scenarios are targeted event sequences of the generated model, named by
// the behaviour they pin down. Scenario inserts the process's internal
// steps (branches, assignments, returns) between them.
var Scenarios = map[string][]string{
	"copy":              {"enqueue", "begin", "readSource_found", "readTarget_found", "writeTarget_ok", "finish"},
	"stale-cache":       {"changeSource_b", "enqueue", "begin", "readSource_found", "deliverCache", "readTarget_found", "writeTarget_ok", "finish"},
	"retained-snapshot": {"enqueue", "begin", "readSource_found", "changeSource_b", "deliverCache", "readTarget_found", "writeTarget_ok", "finish"},
	"cleared-source":    {"changeSource_empty", "deliverCache", "enqueue", "begin", "readSource_found", "readTarget_found", "writeTarget_ok", "finish"},
	"dirty":             {"enqueue", "begin", "enqueue", "enqueue", "readSource_found", "readTarget_found", "writeTarget_ok", "finish", "begin", "readSource_found", "readTarget_found", "writeTarget_ok", "finish"},
	"retry":             {"enqueue", "begin", "readSource_found", "readTarget_found", "writeTarget_error", "finishError", "advance", "advance", "fire", "begin", "readSource_found", "readTarget_found", "writeTarget_ok", "finish"},
	"source-error":      {"enqueue", "begin", "readSource_error", "finishError"},
	"target-error":      {"enqueue", "begin", "readSource_found", "readTarget_error", "finishError"},
}

// Scenario builds a trace of m with original CEL from events, taking the
// one enabled internal process step (an action named node<id>_<kind>,
// listed in spec) whenever the next event is not yet enabled.
func Scenario(spec model.Spec, events ...string) (conformance.Trace, error) {
	m, err := model.Compile(spec)
	if err != nil {
		return conformance.Trace{}, err
	}
	var internal []string
	for _, a := range spec.Actions {
		if _, ok := conformance.NodeID(a.Name); ok {
			internal = append(internal, a.Name)
		}
	}
	state, err := m.Initial()
	if err != nil {
		return conformance.Trace{}, err
	}
	t := conformance.Trace{Status: "ok", States: []conformance.State{{Event: "init", Values: state}}}
	for _, event := range events {
		for steps := 0; ; steps++ {
			if next, err := m.Next(state, event); err == nil {
				state = next
				t.States = append(t.States, conformance.State{Event: event, Values: state})
				break
			}
			if steps == len(internal) {
				return t, fmt.Errorf("event %s is not enabled after %d states", event, len(t.States))
			}
			var enabled []string
			var after map[string]any
			for _, a := range internal {
				if next, err := m.Next(state, a); err == nil {
					enabled, after = append(enabled, a), next
				}
			}
			if len(enabled) != 1 {
				return t, fmt.Errorf("event %s is not enabled and internal steps %v are", event, enabled)
			}
			state = after
			t.States = append(t.States, conformance.State{Event: enabled[0], Values: state})
		}
	}
	return t, nil
}

// Replay replays one trace of m through a fresh adapter, comparing the
// variables the adapter observes.
func Replay(ctx context.Context, m *model.Compiled, trace conformance.Trace) (conformance.Report, error) {
	r, _, err := replay(ctx, m, trace)
	return r, err
}

// replay is Replay that, on a conformance failure, also lists the observed
// variables whose projection differs from the failing trace state.
func replay(ctx context.Context, m *model.Compiled, trace conformance.Trace) (conformance.Report, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	a, err := New(ctx, m)
	if err != nil {
		return conformance.Report{}, nil, err
	}
	report, err := conformance.ReplayObserved(ctx, m, trace, a, a.Observed())
	var diverged []string
	if f, ok := conformance.AsFailure(err); ok && f.Category == conformance.CategoryConformance && f.Step < len(trace.States) {
		if actual, perr := a.Project(ctx); perr == nil {
			for _, name := range a.Observed() {
				if !reflect.DeepEqual(actual[name], trace.States[f.Step].Values[name]) {
					diverged = append(diverged, name)
				}
			}
		}
	}
	closed := a.Close()
	if err == nil {
		err = closed
	}
	return report, diverged, err
}

// Detect replays the committed evidence of the example at root (the
// generated model in SpecFile, its sampled traces and the Scenarios)
// through the implementation compiled into the running binary. It returns,
// sorted, the observed variables whose projection diverged from the model
// and the failure category of any replay that failed otherwise: what a
// mutation of the implementation is detected by. An unmutated build
// returns nothing.
func Detect(ctx context.Context, root string) ([]string, error) {
	var spec model.Spec
	if _, err := krm.Read(filepath.Join(root, SpecFile), "TransitionModel", &spec); err != nil {
		return nil, err
	}
	m, err := model.Compile(spec)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(root, TraceGlob))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no traces under %s", root)
	}
	var traces []conformance.Trace
	for _, p := range paths {
		t, err := conformance.ReadITF(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		traces = append(traces, t)
	}
	names := make([]string, 0, len(Scenarios))
	for name := range Scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t, err := Scenario(spec, Scenarios[name]...)
		if err != nil {
			return nil, fmt.Errorf("scenario %s: %w", name, err)
		}
		traces = append(traces, t)
	}
	found := map[string]bool{}
	for _, t := range traces {
		_, diverged, err := replay(ctx, m, t)
		if err == nil {
			continue
		}
		if len(diverged) == 0 {
			found[string(conformance.CategoryOf(err))] = true
		}
		for _, name := range diverged {
			found[name] = true
		}
	}
	out := make([]string, 0, len(found))
	for name := range found {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// detectPackage is the command, relative to the module root, that prints
// Detect's result for a root directory.
const detectPackage = "./examples/configmirror/detect"

// RunMutations runs the MutationSuite at root/mutations.yaml with the
// model and replay detectors (see RunMutationDetectors). A mutation's row
// lists what either detector reported.
func RunMutations(ctx context.Context, moduleRoot, root string) (mutation.Suite, mutation.Matrix, error) {
	s, matrix, _, err := RunMutationDetectors(ctx, moduleRoot, root)
	return s, matrix, err
}

// RunMutationDetectors runs the MutationSuite at root/mutations.yaml and
// records which detector reported each name. Nothing is written to the
// tree. The model detector regenerates the model from the mutated source
// and checks its properties on every Scenario. The replay detector replays
// the committed evidence through the mutated implementation (see
// ReplayDetector).
func RunMutationDetectors(ctx context.Context, moduleRoot, root string) (mutation.Suite, mutation.Matrix, mutation.Detections, error) {
	s, err := mutation.Load(filepath.Join(root, MutationsFile))
	if err != nil {
		return s, nil, nil, err
	}
	replay, err := ReplayDetector(ctx, moduleRoot, root)
	if err != nil {
		return s, nil, nil, err
	}
	matrix, detections, err := mutation.RunDetectors(s, ModelDetector(root), replay)
	return s, matrix, detections, err
}

// ModelDetector regenerates the model of the example at root from the
// mutated source and checks its properties on every Scenario of the
// regenerated model. A scenario the mutant cannot run is an error.
func ModelDetector(root string) mutation.Detector {
	return mutation.ModelDetector(func(overlay map[string][]byte) (*model.Compiled, []conformance.Trace, error) {
		spec, err := GenerateOverlay(root, overlay)
		if err != nil {
			return nil, nil, err
		}
		m, err := model.Compile(spec)
		if err != nil {
			return nil, nil, err
		}
		names := make([]string, 0, len(Scenarios))
		for name := range Scenarios {
			names = append(names, name)
		}
		sort.Strings(names)
		var traces []conformance.Trace
		for _, name := range names {
			t, err := Scenario(spec, Scenarios[name]...)
			if err != nil {
				return nil, nil, fmt.Errorf("scenario %s: %w", name, err)
			}
			traces = append(traces, t)
		}
		return m, traces, nil
	})
}

// ReplayDetector is the detector named "replay". It places each mutated
// file in a build overlay and compiles the detect command with it, using
// the go command found on PATH from moduleRoot. The command replays root's
// committed evidence and prints what Detect reported.
func ReplayDetector(ctx context.Context, moduleRoot, root string) (mutation.Detector, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return mutation.Detector{}, err
	}
	detect := func(mutated map[string][]byte) ([]string, error) {
		dir, err := os.MkdirTemp("", "configmirror-mutant-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		replace := map[string]string{}
		for original, contents := range mutated {
			mutant := filepath.Join(dir, fmt.Sprintf("%d-%s", len(replace), filepath.Base(original)))
			if err = os.WriteFile(mutant, contents, 0o600); err != nil {
				return nil, err
			}
			replace[original] = mutant
		}
		overlay, err := json.Marshal(map[string]any{"Replace": replace})
		if err != nil {
			return nil, err
		}
		overlayPath := filepath.Join(dir, "overlay.json")
		if err = os.WriteFile(overlayPath, overlay, 0o600); err != nil {
			return nil, err
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "go", "run", "-overlay", overlayPath, detectPackage, "-root", absRoot)
		cmd.Dir = moduleRoot
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err = cmd.Run(); err != nil {
			return nil, fmt.Errorf("build: %w\n%s", err, stderr.String())
		}
		return strings.Fields(stdout.String()), nil
	}
	return mutation.Detector{Name: "replay", Detect: detect}, nil
}

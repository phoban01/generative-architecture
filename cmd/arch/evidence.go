package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/project"
	"generative-architecture/pkg/receipt"
	"generative-architecture/pkg/report"
)

// Files arch check and arch replay read and write under --out.
const (
	tracesDir    = "traces"
	checkReport  = "report.yaml"
	replayReport = "replay.yaml"
)

// evidence is a generated model with the receipt of its inputs.
type evidence struct {
	project  project.Project
	spec     model.Spec
	model    *model.Compiled
	modelSHA string
	receipt  generationReceipt
}

// loadEvidence reads what arch generate wrote and compares the receipt
// with the current inputs. Each drifted input is a tool finding: the model
// no longer derives from the inputs, so evidence about it is invalid.
func loadEvidence(g globals) (evidence, []report.Finding, error) {
	var ev evidence
	p, err := project.Load(g.project)
	if err != nil {
		return ev, nil, err
	}
	ev.project = p
	irPath := filepath.Join(g.out, irFile)
	doc, err := ir.Read(irPath)
	if errors.Is(err, os.ErrNotExist) {
		return ev, nil, fmt.Errorf("%s not found; run arch extract and arch generate first", irPath)
	}
	if err != nil {
		return ev, nil, err
	}
	if err := checkSupport(g, p, doc); err != nil {
		return ev, nil, err
	}
	specPath := filepath.Join(g.out, specFile)
	if _, err := os.Stat(specPath); errors.Is(err, os.ErrNotExist) {
		return ev, nil, fmt.Errorf("%s not found; run arch generate first", specPath)
	}
	if _, err := krm.Read(specPath, "TransitionModel", &ev.spec); err != nil {
		return ev, nil, err
	}
	if ev.model, err = model.Compile(ev.spec); err != nil {
		return ev, nil, fmt.Errorf("%s: %w", specPath, err)
	}
	ev.modelSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(ev.model.Quint())))
	if ev.receipt, err = readReceipt(filepath.Join(g.out, receiptsFile)); err != nil {
		return ev, nil, err
	}
	ev.receipt.Root = p.Root
	drift, err := receipt.Verify(ev.receipt.Receipt)
	if err != nil {
		return ev, nil, err
	}
	var findings []report.Finding
	for _, d := range drift {
		findings = append(findings, report.Finding{Category: report.CategoryTool, Message: fmt.Sprintf("input %s since arch generate; run arch generate again", d)})
	}
	for _, f := range ev.receipt.Generated {
		b, err := os.ReadFile(filepath.Join(g.out, f.Path))
		if err != nil {
			findings = append(findings, report.Finding{Category: report.CategoryTool, Message: fmt.Sprintf("generated input %s: %v", f.Path, err)})
		} else if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != f.SHA256 {
			findings = append(findings, report.Finding{Category: report.CategoryTool, Message: fmt.Sprintf("generated input %s changed since arch generate; run arch generate again", f.Path)})
		}
	}
	return ev, findings, nil
}

// verification starts a report on the evidence. tools adds the exact
// versions of the tools the run used to the receipt's.
func (ev evidence) verification(scope string, tools map[string]string) report.Verification {
	r := ev.receipt.Receipt
	r.Tools = map[string]string{}
	for name, version := range ev.receipt.Tools {
		r.Tools[name] = version
	}
	for name, version := range tools {
		r.Tools[name] = version
	}
	return report.Verification{Name: ev.project.Name, Scope: scope, Model: modelFile, ModelSHA256: ev.modelSHA, Coverage: map[string]int{}, TraceHashes: map[string]string{}, Receipt: r, Generated: ev.receipt.Generated}
}

// replayModel replays one trace through the original CEL of m. A state
// that is not the CEL successor of its predecessor is a conformance
// finding; a state that violates a property is a safety finding. It
// returns the transitions it evaluated and stops at the first finding.
func replayModel(m *model.Compiled, name string, t conformance.Trace, coverage map[string]int) (int, *report.Finding) {
	finding := func(category string, step int, event string, err error) *report.Finding {
		return &report.Finding{Category: category, Trace: name, Step: step, Event: event, Message: err.Error()}
	}
	if len(t.States) == 0 || t.States[0].Event != "init" {
		return 0, finding(report.CategoryConformance, 0, "", fmt.Errorf("trace does not begin at init"))
	}
	state, err := m.Initial()
	if err != nil {
		return 0, finding(report.CategoryTool, 0, "init", err)
	}
	transitions := 0
	for i, s := range t.States {
		if i > 0 {
			if state, err = m.Next(state, s.Event); err != nil {
				return transitions, finding(report.CategoryConformance, i, s.Event, fmt.Errorf("the model does not allow %s here: %w", s.Event, err))
			}
			transitions++
			coverage[s.Event]++
		}
		if diff := differing(state, s.Values); len(diff) > 0 {
			return transitions, finding(report.CategoryConformance, i, s.Event, fmt.Errorf("trace and model disagree on %v", diff))
		}
		if err := m.Check(state, s.Event); err != nil {
			return transitions, finding(report.CategorySafety, i, s.Event, err)
		}
	}
	return transitions, nil
}

// differing lists, sorted, the variables whose values differ.
func differing(want, got map[string]any) []string {
	var out []string
	for name, v := range want {
		if g, ok := got[name]; !ok || !reflect.DeepEqual(g, v) {
			out = append(out, name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// conclude writes the report and turns its result into the exit status:
// a failure is an error; an incomplete result is an error in verification
// mode and a note on stderr in inventory mode.
func conclude(g globals, v report.Verification, path, command string) error {
	result := v.Conclude()
	if err := v.Write(path); err != nil {
		return err
	}
	switch result {
	case report.Failed:
		for _, f := range v.Findings {
			if f.Category != report.CategoryUnsupported {
				return fmt.Errorf("arch %s: %s finding: %s (see %s)", command, f.Category, f.Message, path)
			}
		}
	case report.Incomplete:
		if g.strict {
			return fmt.Errorf("arch %s: %w: %s (see %s)", command, errStrict, v.Findings[0].Message, path)
		}
		fmt.Fprintf(g.stderr, "inventory mode: %d unsupported findings in %s; --strict fails on them\n", len(v.Findings), path)
	}
	summary(g.stdout, command, v)
	return nil
}

func summary(w io.Writer, command string, v report.Verification) {
	fmt.Fprintf(w, "%s %s: %d traces, %d transitions, %d findings\n", command, v.Result, v.Traces, v.Transitions, len(v.Findings))
}

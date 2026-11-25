package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/mutation"
	"github.com/phoban01/generative-architecture/pkg/quint"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// runMutations runs a mutation suite with two detectors and returns the
// detection matrix, which detector reported each name, and the findings:
// one per escaped expectation, and a tool finding for a detector that
// could not run. The model detector regenerates the model from the
// mutated source through a load overlay, samples the mutant with Quint
// under the run's options and names every property a sampled state
// violates. The replay detector, when arch generate wrote an adapter,
// builds the adapter against the mutated source through a build overlay,
// replays the run's traces through it and names the observed variables
// that diverged and the properties the implementation violated. Nothing
// is written to the module's tree.
func runMutations(g globals, ev evidence, suite mutation.Suite, opts quint.Options, hasAdapter bool) (mutation.Matrix, mutation.Detections, []report.Finding) {
	detectors := []mutation.Detector{mutation.ModelDetector(func(overlay map[string][]byte) (*model.Compiled, []conformance.Trace, error) {
		gen, err := generateWith(g, overlay)
		if err != nil {
			return nil, nil, err
		}
		if gen.spec == nil {
			return nil, nil, fmt.Errorf("the mutant has no model")
		}
		m, err := model.Compile(*gen.spec)
		if err != nil {
			return nil, nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		result, err := quint.Sample(ctx, m, opts)
		if err != nil {
			// A mutant whose sampling finds a violation is detected by
			// the violated property; the counterexample is a trace of it.
			if result.Counterexample.Status != "" {
				return m, []conformance.Trace{result.Counterexample}, nil
			}
			return nil, nil, err
		}
		return m, result.Traces, nil
	})}
	if hasAdapter {
		paths, err := filepath.Glob(filepath.Join(g.out, tracesDir, "trace-*.itf.json"))
		if err != nil {
			return nil, nil, []report.Finding{{Category: report.CategoryTool, Message: err.Error()}}
		}
		detectors = append(detectors, mutation.Detector{Name: "replay", Detect: func(overlay map[string][]byte) ([]string, error) {
			tmp, err := os.MkdirTemp("", "arch-mutant-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(tmp)
			extra := map[string]string{}
			for original, contents := range overlay {
				mutant := filepath.Join(tmp, fmt.Sprintf("%d-%s", len(extra), filepath.Base(original)))
				if err := os.WriteFile(mutant, contents, 0o600); err != nil {
					return nil, err
				}
				extra[original] = mutant
			}
			results, err := runAdapter(g, ev, paths, extra)
			if err != nil {
				return nil, err
			}
			found := map[string]bool{}
			for _, r := range results {
				for _, name := range r.Diverged {
					found[name] = true
				}
				if r.Property != "" {
					found[r.Property] = true
				}
			}
			names := make([]string, 0, len(found))
			for name := range found {
				names = append(names, name)
			}
			sort.Strings(names)
			return names, nil
		}})
	}
	matrix, detections, err := mutation.RunDetectors(suite, detectors...)
	if err != nil {
		return nil, nil, []report.Finding{{Category: report.CategoryTool, Message: "mutation suite: " + err.Error()}}
	}
	var findings []report.Finding
	for _, escaped := range suite.Escaped(matrix) {
		findings = append(findings, report.Finding{Category: report.CategoryConformance, Message: fmt.Sprintf("mutation %s escaped its detectors (CON-8)", escaped)})
	}
	return matrix, detections, findings
}

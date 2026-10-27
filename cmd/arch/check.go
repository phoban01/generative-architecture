package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/mutation"
	"generative-architecture/pkg/quint"
	"generative-architecture/pkg/report"
)

// checkTimeout bounds one arch check run, Quint included.
const checkTimeout = 10 * time.Minute

// runCheck samples traces of the generated model with Quint, evaluates the
// model's properties on every sampled state, replays the traces through
// the original CEL (the differential check) and writes the categorized
// VerificationReport to <out>/report.yaml. The traces go to <out>/traces
// for arch replay. A mutation suite given with --mutations is listed, each
// mutation as unsupported: arch has no replay adapter to detect it with.
func runCheck(g globals, args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	samples := fs.Int("samples", 40, "number of Quint traces to sample")
	steps := fs.Int("steps", 60, "maximum steps per trace")
	seed := fs.Int64("seed", 1, "Quint random seed")
	mutations := fs.String("mutations", "", "MutationSuite document to list in the report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("arch check: unexpected arguments %v", fs.Args())
	}
	if *samples <= 0 || *steps <= 0 {
		return fmt.Errorf("arch check: --samples and --steps must be positive")
	}
	var suite *mutation.Suite
	if *mutations != "" {
		s, err := mutation.Load(*mutations)
		if err != nil {
			return fmt.Errorf("arch check: %w", err)
		}
		suite = &s
	}
	ev, findings, err := loadEvidence(g)
	if err != nil {
		return fmt.Errorf("arch check: %w", err)
	}
	sampling := report.Sampling{Samples: *samples, Steps: *steps, Seed: *seed}
	scope := fmt.Sprintf("bounded simulation of the generated model with Quint: %d traces of at most %d steps from seed %d, properties checked on every sampled state; not exhaustive verification; no implementation replay", *samples, *steps, *seed)
	tools := map[string]string{"go": runtime.Version()}
	if len(findings) == 0 {
		var version string
		version, findings = sample(g, ev, sampling)
		if version != "" {
			tools["quint"] = version
		}
	}
	v := ev.verification(scope, tools)
	v.Sampling = &sampling
	v.Findings = findings
	if len(findings) == 0 {
		v.Findings = check(g, ev, &v)
	}
	if suite != nil {
		for _, m := range suite.Spec.Mutations {
			v.Findings = append(v.Findings, report.Finding{Category: report.CategoryUnsupported, Message: fmt.Sprintf("mutation %s: no adapter; arch cannot replay a mutated controller", m.Name)})
		}
	}
	return conclude(g, v, filepath.Join(g.out, checkReport), "check")
}

// sample runs Quint and stores the traces under <out>/traces, replacing
// earlier ones. It returns the Quint version, when known, and the
// findings: a violated invariant is a safety finding, any other failure
// (a missing binary, a version outside the supported range) a tool
// finding.
func sample(g globals, ev evidence, s report.Sampling) (string, []report.Finding) {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	result, err := quint.Sample(ctx, ev.model, quint.Options{Samples: s.Samples, Steps: s.Steps, Seed: s.Seed})
	if err != nil {
		category := report.CategoryTool
		if strings.Contains(err.Error(), "[violation]") {
			category = report.CategorySafety
		}
		return result.Version, []report.Finding{{Category: category, Message: fmt.Sprintf("quint: %s", firstLines(err.Error(), 6))}}
	}
	dir := filepath.Join(g.out, tracesDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return result.Version, []report.Finding{{Category: report.CategoryTool, Message: err.Error()}}
	}
	old, _ := filepath.Glob(filepath.Join(dir, "*.itf.json"))
	for _, p := range old {
		if err := os.Remove(p); err != nil {
			return result.Version, []report.Finding{{Category: report.CategoryTool, Message: err.Error()}}
		}
	}
	for i, b := range result.ITF {
		if err := os.WriteFile(filepath.Join(dir, traceName(i)), b, 0o644); err != nil {
			return result.Version, []report.Finding{{Category: report.CategoryTool, Message: err.Error()}}
		}
	}
	return result.Version, nil
}

// check evaluates the properties on the sampled traces and runs the
// differential check, filling the report's counts.
func check(g globals, ev evidence, v *report.Verification) []report.Finding {
	paths, err := filepath.Glob(filepath.Join(g.out, tracesDir, "trace-*.itf.json"))
	if err != nil || len(paths) == 0 {
		return []report.Finding{{Category: report.CategoryTool, Message: "no sampled traces"}}
	}
	var findings []report.Finding
	var traces []conformance.Trace
	for _, p := range paths {
		name := filepath.Base(p)
		b, err := os.ReadFile(p)
		if err != nil {
			return []report.Finding{{Category: report.CategoryTool, Trace: name, Message: err.Error()}}
		}
		v.TraceHashes[name] = fmt.Sprintf("%x", sha256.Sum256(b))
		t, err := conformance.ReadITF(p)
		if err != nil {
			return []report.Finding{{Category: report.CategoryTool, Trace: name, Message: err.Error()}}
		}
		traces = append(traces, t)
		// Properties first, so a violation is reported as safety rather
		// than as a disagreement between Quint and CEL.
		for i, s := range t.States {
			if err := ev.model.Check(s.Values, s.Event); err != nil {
				findings = append(findings, report.Finding{Category: report.CategorySafety, Trace: name, Step: i, Event: s.Event, Message: err.Error()})
				break
			}
		}
	}
	v.Traces = len(traces)
	if len(findings) > 0 {
		return findings
	}
	stats, err := quint.Differential(ev.model, traces)
	v.Transitions, v.Coverage = stats.Transitions, stats.Coverage
	if err != nil {
		f := report.Finding{Category: string(conformance.CategoryOf(err)), Message: err.Error()}
		var failure *conformance.Failure
		if errors.As(err, &failure) {
			f.Step, f.Event = failure.Step, failure.Event
		}
		return []report.Finding{f}
	}
	return nil
}

func traceName(i int) string { return fmt.Sprintf("trace-%03d.itf.json", i) }

// firstLines keeps the first n lines of a tool's output.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, " | ")
}

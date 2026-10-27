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

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/mutation"
	"github.com/phoban01/generative-architecture/pkg/quint"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// checkTimeout bounds one arch check run, Quint included.
const checkTimeout = 10 * time.Minute

// runCheck samples traces of the generated model with Quint, evaluates the
// model's properties on every sampled state, replays the traces through
// the original CEL (the differential check) and writes the categorized
// VerificationReport to <out>/report.yaml. The traces go to <out>/traces
// for arch replay. A mutation suite given with --mutations is listed, each
// mutation as unsupported: arch has no replay adapter to detect it with.
// Temporal properties of the constraints are unchecked unless --verify
// model-checks them with quint verify (see checkTemporal).
func runCheck(g globals, args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	samples := fs.Int("samples", 40, "number of Quint traces to sample")
	steps := fs.Int("steps", 60, "maximum steps per trace")
	seed := fs.Int64("seed", 1, "Quint random seed")
	mutations := fs.String("mutations", "", "MutationSuite document to list in the report")
	steerWeight := fs.Int("steer-weight", 0, "weight of internal process steps and effect outcomes against other actions (0: uniform sampling)")
	steerExcept := fs.String("steer-except", "", "comma-separated effect outcomes that keep weight 1 under --steer-weight, for example error")
	verify := fs.Bool("verify", false, "model-check the temporal properties with quint verify (TLC, needs Java 21) under the declared fairness")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("arch check: unexpected arguments %v", fs.Args())
	}
	if *samples <= 0 || *steps <= 0 {
		return fmt.Errorf("arch check: --samples and --steps must be positive")
	}
	if *steerExcept != "" && *steerWeight == 0 {
		return fmt.Errorf("arch check: --steer-except needs --steer-weight")
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
	// Inputs that drifted since arch generate make the model stale.
	stale := len(findings) > 0
	liveness, err := loadTemporal(ev)
	if err != nil && !stale {
		return fmt.Errorf("arch check: %w", err)
	}
	sampling := report.Sampling{Samples: *samples, Steps: *steps, Seed: *seed}
	opts := quint.Options{Samples: *samples, Steps: *steps, Seed: *seed}
	if *steerWeight != 0 && len(findings) == 0 {
		var except []string
		if *steerExcept != "" {
			except = strings.Split(*steerExcept, ",")
		}
		if opts.Steering, err = quint.PreferProcess(ev.model, *steerWeight, except...); err != nil {
			return fmt.Errorf("arch check: --steer-weight: %w", err)
		}
	}
	scope := fmt.Sprintf("bounded simulation of the generated model with Quint: %d traces of at most %d steps from seed %d, %s sampling, properties checked on every sampled state; not exhaustive verification; no implementation replay", *samples, *steps, *seed, opts.Steering.Strategy())
	tools := map[string]string{"go": runtime.Version()}
	if len(findings) == 0 {
		var version string
		version, findings = sample(g, ev, opts)
		if version != "" {
			tools["quint"] = version
		}
	}
	v := ev.verification(scope, tools)
	v.Sampling = &sampling
	record := opts.Record()
	v.Receipt.Sampling = &record
	v.Findings = findings
	if len(findings) == 0 {
		v.Findings = check(g, ev, &v)
	}
	if suite != nil {
		for _, m := range suite.Spec.Mutations {
			v.Findings = append(v.Findings, report.Finding{Category: report.CategoryUnsupported, Message: fmt.Sprintf("mutation %s: no adapter; arch cannot replay a mutated controller", m.Name)})
		}
	}
	if len(liveness.Temporal) > 0 {
		reason := ""
		switch {
		case !*verify:
			reason = "not model-checked: arch check runs quint verify only with --verify; sampling never checks liveness"
		case stale:
			reason = "not model-checked: the generated model is stale; run arch generate again"
		}
		var temporal []report.Finding
		v.Temporal, temporal = checkTemporal(ev, liveness, reason, quint.Options{})
		v.Findings = append(v.Findings, temporal...)
		v.Scope += "; temporal properties are listed with their own status, never checked by sampling"
	}
	err = conclude(g, v, filepath.Join(g.out, checkReport), "check")
	v.WriteTemporal(g.stdout)
	return err
}

// verifyTimeout bounds one quint verify run of one temporal property.
const verifyTimeout = 5 * time.Minute

// loadTemporal reads the temporal properties and the fairness of every
// constraints document and validates each property against the model. An
// invalid definition is an error. A valid one without fairness is not: it
// is reported as unchecked.
func loadTemporal(ev evidence) (model.Constraints, error) {
	var out model.Constraints
	var paths []string
	for _, rel := range ev.project.Spec.Constraints {
		var doc model.Constraints
		path := filepath.Join(ev.project.Dir, rel)
		if _, err := krm.Read(path, constraintsKind, &doc); err != nil {
			return out, err
		}
		out.Temporal = append(out.Temporal, doc.Temporal...)
		for range doc.Temporal {
			paths = append(paths, path)
		}
		out.Fairness.Weak = append(out.Fairness.Weak, doc.Fairness.Weak...)
		out.Fairness.Strong = append(out.Fairness.Strong, doc.Fairness.Strong...)
	}
	names := map[string]bool{}
	for i, t := range out.Temporal {
		if names[t.Name] {
			return out, fmt.Errorf("%s: temporal %s: declared twice", paths[i], t.Name)
		}
		names[t.Name] = true
		if _, err := ev.model.TemporalModule(t, out.Fairness); err != nil && !errors.Is(err, model.ErrNoFairness) {
			return out, fmt.Errorf("%s: %w", paths[i], err)
		}
	}
	return out, nil
}

// checkTemporal gives every temporal property a status. A non-empty
// reason marks them all unchecked. Otherwise each property with fairness
// runs under quint verify: a completed run makes it verified or violated,
// and any other result leaves it unchecked with the checker's error. A
// violation is a safety finding and an unchecked property an unsupported
// finding, so the report never passes with an unchecked property.
func checkTemporal(ev evidence, c model.Constraints, reason string, opts quint.Options) ([]report.TemporalResult, []report.Finding) {
	if reason == "" && c.Fairness.Empty() {
		reason = model.ErrNoFairness.Error()
	}
	var results []report.TemporalResult
	var findings []report.Finding
	for _, t := range c.Temporal {
		r := report.TemporalResult{Name: t.Name, Weak: c.Fairness.Weak, Strong: c.Fairness.Strong, Reason: reason}
		if r.Reason == "" {
			ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
			verdict, err := quint.Verify(ctx, ev.model, t, c.Fairness, opts)
			cancel()
			if err != nil {
				r.Reason = "quint verify did not complete: " + firstLines(err.Error(), 4)
			} else {
				r.Backend = fmt.Sprintf("quint %s verify --backend %s", verdict.Version, quint.VerifyBackend)
				r.Bound = fmt.Sprintf("%s; time limit %s; Java heap %s", quint.VerifyBound, verifyTimeout, quint.VerifyHeap)
				r.Status = report.TemporalVerified
				if verdict.Outcome == quint.Violated {
					r.Status = report.TemporalViolated
					findings = append(findings, report.Finding{Category: report.CategorySafety, Property: t.Name, Message: fmt.Sprintf("temporal property %s violated under fairness %s: %s", t.Name, c.Fairness, verdict.Detail)})
				}
			}
		}
		if r.Reason != "" {
			r.Status = report.TemporalUnchecked
			findings = append(findings, report.Finding{Category: report.CategoryUnsupported, Property: t.Name, Message: fmt.Sprintf("temporal property %s unchecked: %s", t.Name, r.Reason)})
		}
		results = append(results, r)
	}
	return results, findings
}

// sample runs Quint and stores the traces under <out>/traces, replacing
// earlier ones. It returns the Quint version, when known, and the
// findings: a violated invariant is a safety finding, any other failure
// (a missing binary, a version outside the supported range) a tool
// finding.
func sample(g globals, ev evidence, opts quint.Options) (string, []report.Finding) {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	result, err := quint.Sample(ctx, ev.model, opts)
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
				findings = append(findings, report.Finding{Category: report.CategorySafety, Trace: name, Step: i, Event: s.Event, Property: violatedProperty(err), Message: err.Error()})
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

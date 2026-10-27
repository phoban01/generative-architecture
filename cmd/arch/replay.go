package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// noAdapter is the unsupported finding of every replay: arch replays
// traces against the generated model, but it has no adapter that drives
// the controller, so the implementation side is not replayed.
const noAdapter = "no adapter: arch replays traces against the generated model only; replaying them through the controller needs a replay adapter, which arch cannot build yet"

// runReplay replays saved ITF traces (by default those arch check sampled)
// against the generated model: every state must be the CEL successor of
// the one before and satisfy the properties. It writes the categorized
// VerificationReport to <out>/replay.yaml. The implementation is not
// replayed; the report says so with an unsupported finding.
func runReplay(g globals, args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	glob := fs.String("traces", "", "glob of ITF traces to replay (default <out>/traces/*.itf.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("arch replay: unexpected arguments %v", fs.Args())
	}
	if *glob == "" {
		*glob = filepath.Join(g.out, tracesDir, "*.itf.json")
	}
	paths, err := filepath.Glob(*glob)
	if err != nil {
		return fmt.Errorf("arch replay: %w", err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("arch replay: no traces match %s; run arch check first or pass --traces", *glob)
	}
	sort.Strings(paths)
	ev, findings, err := loadEvidence(g)
	if err != nil {
		return fmt.Errorf("arch replay: %w", err)
	}
	scope := fmt.Sprintf("replay of %d saved traces against the generated model; no implementation replay", len(paths))
	v := ev.verification(scope, map[string]string{"go": runtime.Version()})
	v.Findings = findings
	if len(findings) == 0 {
		for _, p := range paths {
			name := filepath.Base(p)
			b, err := os.ReadFile(p)
			if err != nil {
				return fmt.Errorf("arch replay: %w", err)
			}
			v.TraceHashes[name] = fmt.Sprintf("%x", sha256.Sum256(b))
			t, err := conformance.ReadITF(p)
			if err != nil {
				v.Findings = append(v.Findings, report.Finding{Category: report.CategoryTool, Trace: name, Message: err.Error()})
				continue
			}
			n, f := replayModel(ev.model, name, t, v.Coverage)
			v.Traces++
			v.Transitions += n
			if f != nil {
				v.Findings = append(v.Findings, *f)
			}
		}
	}
	v.Findings = append(v.Findings, report.Finding{Category: report.CategoryUnsupported, Message: noAdapter})
	return conclude(g, v, filepath.Join(g.out, replayReport), "replay")
}

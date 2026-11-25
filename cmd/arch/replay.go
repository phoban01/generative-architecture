package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/composition"
	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// noAdapter is the unsupported finding of a replay without a generated
// adapter: arch replays the traces against the generated model only.
const noAdapter = "no adapter: arch generate wrote no replay adapter under %s, so the traces were replayed against the generated model only; arch generate says why"

// runReplay replays saved ITF traces (by default those arch check sampled)
// against the generated model: every state must be the CEL successor of
// the one before and satisfy the properties. When arch generate wrote a
// replay adapter, it builds the adapter in the project's module and
// replays the traces through the implementation as well. It writes the
// categorized VerificationReport to <out>/replay.yaml.
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
	adapter := filepath.Join(g.out, adapterDir, adapterMain)
	_, adapterErr := os.Stat(adapter)
	hasAdapter := adapterErr == nil
	scope := fmt.Sprintf("replay of %d saved traces against the generated model; no implementation replay", len(paths))
	if hasAdapter {
		scope = fmt.Sprintf("replay of %d saved traces against the generated model and through the implementation, driven by the generated adapter", len(paths))
	}
	v := ev.verification(scope, map[string]string{"go": runtime.Version()})
	v.Bindings, v.Protocols = composition.ReportSections(ev.links, nil, nil)
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
	switch {
	case len(findings) > 0:
	case hasAdapter:
		v.Findings = append(v.Findings, replayImplementation(g, ev, paths)...)
	default:
		v.Findings = append(v.Findings, report.Finding{Category: report.CategoryUnsupported, Message: fmt.Sprintf(noAdapter, filepath.Join(g.out, adapterDir))})
	}
	return conclude(g, v, filepath.Join(g.out, replayReport), "replay")
}

// replayResult is one line of the generated adapter's output.
type replayResult struct {
	Trace    string         `json:"trace"`
	Steps    int            `json:"steps"`
	Coverage map[string]int `json:"coverage"`
	Category string         `json:"category"`
	Step     int            `json:"step"`
	Event    string         `json:"event"`
	Message  string         `json:"message"`
}

// replayImplementation builds the generated adapter in the project's
// module, through a build overlay so the module's tree stays untouched,
// and runs it over the traces. Each failed trace is a finding of the
// adapter's category; a build or run failure is a tool finding.
func replayImplementation(g globals, ev evidence, paths []string) []report.Finding {
	tool := func(err error) []report.Finding {
		return []report.Finding{{Category: report.CategoryTool, Message: "replay adapter: " + err.Error()}}
	}
	dir := filepath.Join(g.out, adapterDir)
	entries, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return tool(err)
	}
	root, err := filepath.Abs(ev.project.Root)
	if err != nil {
		return tool(err)
	}
	outAbs, err := filepath.Abs(g.out)
	if err != nil {
		return tool(err)
	}
	virtual := filepath.Join("internal", fmt.Sprintf("archreplay_%x", sha256.Sum256([]byte(outAbs)))[:16])
	replace := map[string]string{}
	for _, e := range entries {
		replace[filepath.Join(root, virtual, filepath.Base(e))] = e
	}
	overlay, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		return tool(err)
	}
	tmp, err := os.MkdirTemp("", "arch-replay-")
	if err != nil {
		return tool(err)
	}
	defer os.RemoveAll(tmp)
	overlayPath := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		return tool(err)
	}
	args := append([]string{"run", "-overlay", overlayPath, "./" + filepath.ToSlash(virtual), "-model", filepath.Join(outAbs, specFile)}, nil...)
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return tool(err)
		}
		args = append(args, abs)
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return tool(fmt.Errorf("go run in %s: %w\n%s", root, err, strings.TrimSpace(stderr.String())))
	}
	var findings []report.Finding
	dec := json.NewDecoder(&stdout)
	for {
		var r replayResult
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			return tool(fmt.Errorf("adapter output: %w", err))
		}
		if r.Category != "" {
			findings = append(findings, report.Finding{Category: r.Category, Trace: r.Trace, Step: r.Step, Event: r.Event, Message: "implementation: " + r.Message})
		}
	}
	return findings
}

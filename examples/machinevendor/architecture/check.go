package architecture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Evidence struct {
	ModelSHA256 string            `yaml:"modelSha256"`
	Files       map[string]string `yaml:"files"`
}
type CheckOptions struct {
	Replay bool
	Quint  string
}

func RunTool(root, log, exe string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = root
	b, err := cmd.CombinedOutput()
	if log != "" {
		if writeErr := os.WriteFile(filepath.Join(root, "reports", log+".txt"), b, 0644); writeErr != nil {
			return b, writeErr
		}
	}
	if err != nil {
		return b, fmt.Errorf("%s %s: %w\n%s", exe, strings.Join(args, " "), err, b)
	}
	return b, nil
}
func toolName(value, env, fallback string) string {
	if value != "" {
		return value
	}
	if v := os.Getenv(env); v != "" {
		return v
	}
	return fallback
}
func (m *Model) Check(o CheckOptions) (map[string]any, error) {
	p := m.Project
	root := p.Root
	reports := filepath.Join(root, "reports")
	if err := os.MkdirAll(reports, 0755); err != nil {
		return nil, err
	}
	os.Remove(filepath.Join(reports, "summary.yaml"))
	if err := m.Generate(false); err != nil {
		return nil, err
	}
	modelHash := digest([]byte(m.Quint()))
	manifestPath := filepath.Join(root, "traces/manifest.yaml")
	evidence := Evidence{modelHash, map[string]string{}}
	if o.Replay {
		if err := ReadResult(manifestPath, "TraceManifest", &evidence); err != nil {
			return nil, fmt.Errorf("replay requires evidence from a completed arch check: %w", err)
		}
		if evidence.ModelSHA256 != modelHash {
			return nil, fmt.Errorf("trace evidence belongs to a different model; run a fresh check")
		}
		for name, expected := range evidence.Files {
			clean := filepath.Clean(name)
			if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
				return nil, fmt.Errorf("invalid evidence path")
			}
			b, err := os.ReadFile(filepath.Join(root, clean))
			if err != nil {
				return nil, err
			}
			if digest(b) != expected {
				return nil, fmt.Errorf("trace evidence changed: %s", name)
			}
		}
	} else {
		quint := toolName(o.Quint, "QUINT_BIN", "quint")
		version, err := RunTool(root, "quint-version", quint, "--version")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(version)) != "0.33.0" {
			return nil, fmt.Errorf("expected Quint 0.33.0, got %s", version)
		}
		if _, err := RunTool(root, "quint-typecheck", quint, "typecheck", "generated/System.qnt"); err != nil {
			return nil, err
		}
		fmt.Println("PASS Quint typecheck")
		traceDir := filepath.Join(root, "traces")
		if err := os.MkdirAll(traceDir, 0755); err != nil {
			return nil, err
		}
		// Remove only generated ITF files and the tool's receipt; leave unrelated files alone.
		old, _ := filepath.Glob(filepath.Join(traceDir, "*.itf.json"))
		for _, path := range old {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
		}
		os.Remove(manifestPath)
		invariant := []string{"--invariants"}
		for _, pr := range p.System.Properties {
			invariant = append(invariant, pr.Name)
		}
		for _, name := range keys(p.Scenarios) {
			args := []string{"run", "generated/System.qnt", "--backend", "typescript", "--step", name + "Step", "--max-steps", fmt.Sprint(len(p.Scenarios[name])), "--max-samples", "1", "--seed", "42"}
			args = append(args, invariant...)
			args = append(args, "--out-itf", "traces/"+name+".itf.json", "--verbosity", "1")
			if _, err := RunTool(root, "quint-"+name, quint, args...); err != nil {
				return nil, err
			}
		}
		args := []string{"run", "generated/System.qnt", "--backend", "typescript", "--max-steps", "40", "--max-samples", "200", "--n-traces", "200", "--seed", "20261013"}
		args = append(args, invariant...)
		args = append(args, "--out-itf", "traces/random_{seq}.itf.json", "--verbosity", "1")
		if _, err := RunTool(root, "quint-random", quint, args...); err != nil {
			return nil, err
		}
		racePath := filepath.Join(reports, "revocation-race.itf.json")
		os.Remove(racePath)
		_, err = RunTool(root, "revocation-race", quint, "run", "generated/System.qnt", "--backend", "typescript", "--step", "revokedAfterPublicationStep", "--max-steps", fmt.Sprint(len(p.Scenarios["revokedAfterPublication"])), "--max-samples", "1", "--seed", "42", "--invariant", "provisionRequiresCurrentLease", "--out-itf", "reports/revocation-race.itf.json", "--verbosity", "1")
		var exit *exec.ExitError
		if err == nil || !errors.As(err, &exit) {
			return nil, fmt.Errorf("expected Quint invariant-violation exit; got %v", err)
		}
	}
	paths, err := filepath.Glob(filepath.Join(root, "traces/*.itf.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) != 200+len(p.Scenarios) {
		return nil, fmt.Errorf("unexpected trace count %d", len(paths))
	}
	eval, err := NewEvaluator()
	if err != nil {
		return nil, err
	}
	coverage := map[string]int{}
	steps := 0
	recordFile := func(path string) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if o.Replay {
			if evidence.Files[name] != digest(b) {
				return fmt.Errorf("unrecorded or changed trace %s", name)
			}
		} else {
			evidence.Files[name] = digest(b)
		}
		return nil
	}
	for _, path := range paths {
		if err := recordFile(path); err != nil {
			return nil, err
		}
		trace, err := ReadTrace(path)
		if err != nil {
			return nil, err
		}
		if trace.Status != "ok" {
			return nil, fmt.Errorf("expected non-violating input trace")
		}
		if _, err := m.ValidateCEL(trace, eval); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		result, err := m.Replay(trace, false)
		if err != nil {
			return nil, err
		}
		if !result.OK {
			WriteResult(filepath.Join(reports, "unexpected-mismatch.yaml"), "VerificationReport", "unexpected-mismatch", result)
			return nil, fmt.Errorf("Go simulator mismatch in %s", filepath.Base(path))
		}
		expectedSteps := 40
		name := strings.TrimSuffix(filepath.Base(path), ".itf.json")
		if scenario, ok := p.Scenarios[name]; ok {
			expectedSteps = len(scenario)
		}
		if result.Steps != expectedSteps {
			return nil, fmt.Errorf("short or vacuous trace %s", name)
		}
		steps += result.Steps
		for key, n := range result.Coverage {
			coverage[key] += n
		}
	}
	for _, a := range m.Actions {
		names := []string{a.ID}
		if a.ID == "mac.publish" || a.ID == "service.execute" {
			names = []string{a.ID + ":accepted", a.ID + ":rejected"}
		}
		for _, name := range names {
			if coverage[name] == 0 {
				return nil, fmt.Errorf("uncovered outcome %s", name)
			}
		}
	}
	mutations := []map[string]any{}
	for _, name := range []string{"staleLease", "staleEpoch"} {
		t, err := ReadTrace(filepath.Join(root, "traces", name+".itf.json"))
		if err != nil {
			return nil, err
		}
		result, err := m.Replay(t, true)
		if err != nil {
			return nil, err
		}
		if result.OK || result.Action != "mac.publish" || result.ExpectedAccepted || !result.ActualAccepted {
			return nil, fmt.Errorf("mutation not detected as expected: %s", name)
		}
		if err := WriteResult(filepath.Join(reports, "mutation-"+name+".yaml"), "VerificationReport", "mutation-"+strings.ToLower(name), result); err != nil {
			return nil, err
		}
		mutations = append(mutations, map[string]any{"scenario": name, "step": result.Steps, "detected": true})
	}
	racePath := filepath.Join(reports, "revocation-race.itf.json")
	if err := recordFile(racePath); err != nil {
		return nil, err
	}
	race, err := ReadTrace(racePath)
	if err != nil {
		return nil, err
	}
	last := race.States[len(race.States)-1]["event"].(map[string]any)
	if race.Status != "violation" || last["name"] != "service.execute" || last["accepted"] != true || len(race.States)-1 != len(p.Scenarios["revokedAfterPublication"]) {
		return nil, fmt.Errorf("unexpected counterexample shape")
	}
	violations, err := m.ValidateCEL(race, eval)
	if err != nil {
		return nil, err
	}
	if violations["provisionRequiresCurrentLease"] != 1 {
		return nil, fmt.Errorf("CEL did not reproduce the safety counterexample")
	}
	if err := m.WriteTraceDiagram(race); err != nil {
		return nil, err
	}
	if o.Replay && len(evidence.Files) != len(paths)+1 {
		return nil, fmt.Errorf("unexpected evidence manifest entries")
	}
	if !o.Replay {
		if err := WriteResult(manifestPath, "TraceManifest", "system-traces", evidence); err != nil {
			return nil, err
		}
	}
	summary := map[string]any{"status": "conformance-passed; model-safety-gap-demonstrated", "implementation": "Go / cel-go", "scope": "finite authored contracts and simulated Go implementation; no actual CAPI/controller-runtime", "modelCheck": "random invariant simulation, not exhaustive verification", "modelSha256": modelHash, "traceCount": len(paths), "steps": steps, "celEvaluations": eval.Count, "mode": map[bool]string{true: "replay of hash-checked evidence; no external tools run", false: "fresh Quint traces"}[o.Replay], "likec4Validation": "separate render command", "coverage": coverage, "mutations": mutations, "safetyGap": map[string]any{"property": "provisionRequiresCurrentLease", "result": "counterexample", "assumption": "clearing an assignment does not cancel a prepared provisioning call"}}
	if err := WriteResult(filepath.Join(reports, "summary.yaml"), "VerificationReport", "system", summary); err != nil {
		return nil, err
	}
	fmt.Printf("PASS %d traces / %d transitions / %d CEL evaluations; mutations detected and safety counterexample reproduced\n", len(paths), steps, eval.Count)
	return summary, nil
}
func (m *Model) WriteTraceDiagram(t Trace) error {
	var b strings.Builder
	b.WriteString("// GENERATED from the actual Quint counterexample.\nviews {\n  dynamic view revocation_race {\n    variant sequence\n    title 'Quint counterexample: call survives assignment clearing'\n    description 'Actual model trace; assumes queued calls survive assignment clearing. Does not establish a production bug.'\n")
	for _, state := range t.States[1:] {
		e := state["event"].(map[string]any)
		name := e["name"].(string)
		owner := strings.Split(name, ".")[0]
		fmt.Fprintf(&b, "    %s -> %s %s\n", owner, owner, quote(fmt.Sprintf("%s: %s; accepted=%v", name, e["infra"], e["accepted"])))
	}
	b.WriteString("  }\n}\n")
	return os.WriteFile(filepath.Join(m.Project.Root, "generated/revocation-race.c4"), []byte(b.String()), 0644)
}
func (m *Model) Render(binary string) error {
	if err := m.Generate(false); err != nil {
		return err
	}
	os.MkdirAll(filepath.Join(m.Project.Root, "reports"), 0755)
	tool := toolName(binary, "LIKEC4_BIN", "likec4")
	version, err := RunTool(m.Project.Root, "likec4-version", tool, "--version")
	if err != nil {
		return err
	}
	if !strings.Contains(string(version), "1.59.4") {
		return fmt.Errorf("expected LikeC4 1.59.4, got %s", version)
	}
	if _, err := RunTool(m.Project.Root, "likec4-validate", tool, "validate", "generated"); err != nil {
		return err
	}
	_, err = RunTool(m.Project.Root, "likec4-build", tool, "build", "generated", "-o", "site", "--base", "./", "--use-hash-history", "--output-single-file")
	return err
}

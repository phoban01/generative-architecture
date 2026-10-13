package runtimecheck

import (
	"context"
	"errors"
	"fmt"
	"generative-architecture/pkg/krm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func CheckComposition(root, tool string) (map[string]any, error) {
	dir := filepath.Join(root, "reports/composition")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	_ = os.Remove(filepath.Join(dir, "summary.yaml"))
	model, err := GenerateComposition(root)
	if err != nil {
		return nil, err
	}
	if tool == "" {
		tool = os.Getenv("QUINT_BIN")
	}
	if tool == "" {
		tool = "quint"
	}
	run := func(name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, tool, args...)
		cmd.Dir = root
		b, err := cmd.CombinedOutput()
		if e := os.WriteFile(filepath.Join(dir, name+".txt"), b, 0644); e != nil {
			return b, e
		}
		return b, err
	}
	version, err := run("version", "--version")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(version)) != "0.33.0" {
		return nil, fmt.Errorf("expected Quint 0.33.0")
	}
	if out, err := run("typecheck", "typecheck", "generated/composition/Composed.qnt"); err != nil {
		return nil, fmt.Errorf("typecheck: %w: %s", err, out)
	}
	old, err := filepath.Glob(filepath.Join(dir, "trace-*.itf.json"))
	if err != nil {
		return nil, err
	}
	for _, path := range old {
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	}
	scenarios := []string{"successStep", "staleStep", "raceStep", "conflictStep", "conflictRetryStep", "dirtyStep", "readClaimErrorStep", "readInfraErrorStep", "updateStatusErrorStep", "step"}
	for _, step := range scenarios {
		samples, steps := "1", "40"
		trace := "reports/composition/trace-" + step + ".itf.json"
		if step == "step" {
			samples, steps = "100", "80"
			trace = "reports/composition/trace-random-{seq}.itf.json"
		}
		args := []string{"run", "generated/composition/Composed.qnt", "--backend", "typescript", "--step", step, "--max-steps", steps, "--max-samples", samples, "--n-traces", samples, "--seed", "20261013", "--invariant", "composedSafety", "--out-itf", trace, "--verbosity", "1"}
		if out, err := run(step, args...); err != nil {
			return nil, fmt.Errorf("%s: %w: %s", step, err, out)
		}
	}
	paths, err := filepath.Glob(filepath.Join(dir, "trace-*.itf.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) != 109 {
		return nil, fmt.Errorf("expected 109 traces; got %d", len(paths))
	}
	coverage := map[string]int{}
	hashes := map[string]string{}
	total := 0
	for _, path := range paths {
		trace, err := ReadCompositionTrace(path)
		if err != nil {
			return nil, err
		}
		if trace.Status != "ok" {
			return nil, fmt.Errorf("unexpected counterexample %s", path)
		}
		n, err := ReplayComposition(trace)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		total += n
		for _, state := range trace.States[1:] {
			event := state["event"].(string)
			coverage[event]++
			if event == "commit" && state["s"].(map[string]any)["lease"] == false {
				coverage["commit:revoked"]++
			}
			if event == "finish" && state["returnedError"] == true {
				coverage["finish:retry"]++
			}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		hashes[filepath.Base(path)] = digest(b)
	}
	for _, required := range []string{"commit", "commit:revoked", "writeError", "finish:retry", "fire", "readClaim", "readInfra", "return"} {
		if coverage[required] == 0 {
			return nil, fmt.Errorf("missing required coverage: %s", required)
		}
	}
	// The stronger, independently authored live-lease property is expected to fail.
	failure := "reports/composition/live-lease-race.itf.json"
	_ = os.Remove(filepath.Join(root, failure))
	output, err := run("live-lease-race", "run", "generated/composition/Composed.qnt", "--backend", "typescript", "--step", "raceStep", "--max-steps", "40", "--max-samples", "1", "--seed", "42", "--invariant", "publicationUsesLiveLease", "--out-itf", failure, "--verbosity", "1")
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return nil, fmt.Errorf("expected live-lease violation: %v: %s", err, output)
	}
	race, err := ReadCompositionTrace(filepath.Join(root, failure))
	if err != nil {
		return nil, err
	}
	last := race.States[len(race.States)-1]
	if race.Status != "violation" || last["event"] != "commit" || last["s"].(map[string]any)["lease"] != false {
		return nil, fmt.Errorf("expected publication-after-revocation counterexample")
	}
	if _, err = ReplayComposition(race); err != nil {
		return nil, fmt.Errorf("counterexample did not replay against Go: %w", err)
	}
	if err = krm.WriteResult(filepath.Join(dir, "live-lease-race.yaml"), "Counterexample", "composed-live-lease-race", race.States); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(root, failure))
	if err != nil {
		return nil, err
	}
	hashes[filepath.Base(failure)] = digest(b)
	stale, err := ReadCompositionTrace(filepath.Join(dir, "trace-staleStep.itf.json"))
	if err != nil {
		return nil, err
	}
	mutations := map[string]string{}
	for _, mutation := range []string{"cached-authorization", "skip-revalidation"} {
		_, err := replayComposition(stale, mutation)
		if err == nil {
			return nil, fmt.Errorf("mutation escaped: %s", mutation)
		}
		if !strings.Contains(err.Error(), "expected") {
			return nil, fmt.Errorf("mutation failed for an unrelated reason: %w", err)
		}
		mutations[mutation] = err.Error()
	}
	if err = writeCompositionDiagram(root, race); err != nil {
		return nil, err
	}
	receipts, err := compositionHashes(root)
	if err != nil {
		return nil, err
	}
	summary := map[string]any{"result": "expected-outcomes-reproduced", "modelSha256": digest([]byte(model)), "inputHashes": receipts, "traces": len(paths), "transitions": total, "coverage": coverage, "traceHashes": hashes, "mutationsDetected": mutations, "liveLeaseSafety": "violated; counterexample replayed against unmodified MAC.Publish with controlled API/cache environment", "counterexampleSteps": len(race.States) - 1, "conformance": "client reads, submitted writes, API state, snapshots, returns and queue results; pure Go steps are stuttering", "scope": []string{"one key, one worker, one selected process per invocation", "shared FIFO runtime profile; API/cache/timers are controlled", "parent error-forwarding policy declared, not extracted", "sampled execution; no production API-server or liveness proof"}}
	if err = krm.WriteResult(filepath.Join(dir, "summary.yaml"), "VerificationReport", "composed-publication", summary); err != nil {
		return nil, err
	}
	fmt.Printf("PASS composition conformance: %d traces / %d transitions; live-lease counterexample replayed; two mutations detected\n", len(paths), total)
	return summary, nil
}

package runtimecheck

import (
	"context"
	"crypto/sha256"
	"fmt"
	"generative-architecture/pkg/krm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func Check(root, tool string) (map[string]any, error) {
	dir := filepath.Join(root, "reports/runtime")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	_ = os.Remove(filepath.Join(dir, "summary.yaml"))
	model, err := Generate(root)
	if err != nil {
		return nil, err
	}
	if tool == "" {
		tool = os.Getenv("QUINT_BIN")
	}
	if tool == "" {
		tool = "quint"
	}
	run := func(name string, args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, tool, args...)
		cmd.Dir = root
		b, err := cmd.CombinedOutput()
		if e := os.WriteFile(filepath.Join(dir, name+".txt"), b, 0644); e != nil {
			return e
		}
		if err != nil {
			return fmt.Errorf("%s: %w: %s", name, err, b)
		}
		return nil
	}
	if err = run("version", "--version"); err != nil {
		return nil, err
	}
	version, err := os.ReadFile(filepath.Join(dir, "version.txt"))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(version)) != "0.33.0" {
		return nil, fmt.Errorf("expected Quint 0.33.0")
	}
	if err = run("typecheck", "typecheck", "generated/runtime/Runtime.qnt"); err != nil {
		return nil, err
	}
	// Remove only this command's old generated trace files.
	paths, err := filepath.Glob(filepath.Join(dir, "trace-*.itf.json"))
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if err = os.Remove(p); err != nil {
			return nil, err
		}
	}
	for _, step := range []string{"publicationStep", "dirtyStep", "retryStep", "staleCacheStep", "timerSurvivesSuccessStep", "step"} {
		samples, steps := "1", "16"
		trace := "reports/runtime/trace-" + step + ".itf.json"
		if step == "step" {
			samples, steps = "100", "60"
			trace = "reports/runtime/trace-random-{seq}.itf.json"
		}
		args := []string{"run", "generated/runtime/Runtime.qnt", "--backend", "typescript", "--step", step, "--max-steps", steps, "--max-samples", samples, "--n-traces", samples, "--seed", "20261013", "--invariant", "runtimeSafety", "--out-itf", trace, "--verbosity", "1"}
		if err = run(step, args...); err != nil {
			return nil, err
		}
	}
	paths, err = filepath.Glob(filepath.Join(dir, "trace-*.itf.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) != 105 {
		return nil, fmt.Errorf("expected 105 traces, got %d", len(paths))
	}
	total := 0
	coverage := map[string]int{}
	hashes := map[string]string{}
	for _, path := range paths {
		trace, e := ReadTrace(path)
		if e != nil {
			return nil, e
		}
		n, e := Replay(trace)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), e)
		}
		total += n
		for _, s := range trace.States[1:] {
			coverage[s["event"].(string)]++
			if s["event"] == "publish" {
				state := s["s"].(map[string]any)
				if state["accepted"] == true {
					coverage["publish:accepted"]++
				} else {
					coverage["publish:rejected"]++
				}
				if state["lease"] == true && state["observed"] == true && state["accepted"] == false {
					coverage["publish:conflict"]++
				}
			}
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		hashes[filepath.Base(path)] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	if coverage["publish:accepted"] < 2 || coverage["publish:conflict"] < 1 {
		return nil, fmt.Errorf("missing positive/conflict publication coverage")
	}
	path, err := profilePath(root)
	if err != nil {
		return nil, err
	}
	config, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sources, err := sourceHashes(root)
	if err != nil {
		return nil, err
	}
	summary := map[string]any{"sourceHashes": sources, "modelSha256": fmt.Sprintf("%x", sha256.Sum256([]byte(model))), "profileSha256": fmt.Sprintf("%x", sha256.Sum256(config)), "traces": len(paths), "transitions": total, "coverage": coverage, "traceHashes": hashes, "result": "passed", "implementation": "real controller-runtime worker/result handling + client-go FIFO/rate limiter + real MAC Observe/Publish", "abstractions": []string{"controlled cache delivery backed by fake clients", "logical delay delivery", "one key, one worker, fixed retry delay", "Publish is a macro-step here; read/write race tested separately by behavior-demo"}, "notEstablished": []string{"real API-server or informer-network conformance", "priority queue profile", "multi-worker or multi-controller composition", "liveness/fairness", "automatic extraction of manager/builder configuration"}}
	if err = krm.WriteResult(filepath.Join(dir, "summary.yaml"), "VerificationReport", "runtime-conformance", summary); err != nil {
		return nil, err
	}
	fmt.Printf("PASS runtime conformance: %d traces, %d transitions\n", len(paths), total)
	return summary, nil
}

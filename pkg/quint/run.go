// Package quint invokes a separately installed Quint CLI. No Node application
// or generated JavaScript is part of the Go library.
package quint

import (
	"context"
	"fmt"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/model"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Options struct {
	Executable     string
	Samples, Steps int
	Seed           int64
}
type Result struct {
	Version string
	Traces  []conformance.Trace
	ITF     [][]byte
}

// Sample produces bounded simulation traces. It is not exhaustive verification.
// The caller owns timeouts. Version checks fail closed against the supported CLI.
func Sample(ctx context.Context, m *model.Compiled, opts Options) (Result, error) {
	var result Result
	if opts.Samples <= 0 || opts.Steps <= 0 {
		return result, fmt.Errorf("positive sample and step bounds required")
	}
	exe := opts.Executable
	if exe == "" {
		exe = os.Getenv("QUINT_BIN")
	}
	if exe == "" {
		exe = "quint"
	}
	dir, err := os.MkdirTemp("", "architecture-quint-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "Model.qnt")
	if err = os.WriteFile(path, []byte(m.Quint()), 0600); err != nil {
		return result, err
	}
	run := func(args ...string) ([]byte, error) {
		b, e := exec.CommandContext(ctx, exe, args...).CombinedOutput()
		if e != nil {
			return b, fmt.Errorf("quint %v: %w\n%s", args, e, b)
		}
		return b, nil
	}
	b, err := run("--version")
	if err != nil {
		return result, err
	}
	result.Version = strings.TrimSpace(string(b))
	if result.Version != "0.33.0" {
		return result, fmt.Errorf("unsupported Quint version %s; expected 0.33.0", result.Version)
	}
	if _, err = run("typecheck", path); err != nil {
		return result, err
	}
	_, err = run("run", path, "--backend", "typescript", "--max-samples", strconv.Itoa(opts.Samples), "--n-traces", strconv.Itoa(opts.Samples), "--max-steps", strconv.Itoa(opts.Steps), "--seed", strconv.FormatInt(opts.Seed, 10), "--invariant", "safety", "--out-itf", filepath.Join(dir, "trace-{seq}.itf.json"), "--verbosity", "1")
	if err != nil {
		return result, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "trace-*.itf.json"))
	if err != nil {
		return result, err
	}
	if len(paths) != opts.Samples {
		return result, fmt.Errorf("expected %d traces, got %d", opts.Samples, len(paths))
	}
	for _, p := range paths {
		t, err := conformance.ReadITF(p)
		if err != nil {
			return result, err
		}
		if t.Status != "ok" {
			return result, fmt.Errorf("unexpected trace status %s", t.Status)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return result, err
		}
		result.Traces = append(result.Traces, t)
		result.ITF = append(result.ITF, b)
	}
	return result, nil
}

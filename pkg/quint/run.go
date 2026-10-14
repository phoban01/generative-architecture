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

	"golang.org/x/mod/semver"
)

// DefaultMinVersion and DefaultMaxVersion bound the supported Quint CLI
// versions: MinVersion is inclusive and MaxVersion exclusive.
const (
	DefaultMinVersion = "0.33.0"
	DefaultMaxVersion = "0.34.0"
)

type Options struct {
	Executable     string
	Samples, Steps int
	Seed           int64
	// MinVersion (inclusive) and MaxVersion (exclusive) override the default
	// supported range. Leave empty to use DefaultMinVersion/DefaultMaxVersion.
	MinVersion, MaxVersion string
}

// Supported reports whether a `quint --version` string lies within the
// supported range. The range is [MinVersion, MaxVersion) using semver
// ordering; both bounds and the version may omit the leading "v".
func Supported(version string, opts Options) error {
	minV, maxV := opts.MinVersion, opts.MaxVersion
	if minV == "" {
		minV = DefaultMinVersion
	}
	if maxV == "" {
		maxV = DefaultMaxVersion
	}
	v := canonical(version)
	if !semver.IsValid(v) {
		return fmt.Errorf("unsupported Quint version %q: not a semantic version", version)
	}
	if semver.Prerelease(v) != "" {
		return fmt.Errorf("unsupported Quint version %s: pre-release builds are not supported", version)
	}
	lo, hi := canonical(minV), canonical(maxV)
	if !semver.IsValid(lo) || !semver.IsValid(hi) {
		return fmt.Errorf("invalid Quint version range [%s, %s)", minV, maxV)
	}
	if semver.Compare(v, lo) < 0 || semver.Compare(v, hi) >= 0 {
		return fmt.Errorf("unsupported Quint version %s; expected >= %s and < %s", version, minV, maxV)
	}
	return nil
}

func canonical(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
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
	if err = Supported(result.Version, opts); err != nil {
		return result, err
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

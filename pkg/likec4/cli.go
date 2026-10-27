package likec4

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/mod/semver"
)

// DefaultMinVersion and DefaultMaxVersion bound the supported LikeC4 CLI
// versions (ADR-0003): MinVersion is inclusive and MaxVersion exclusive.
const (
	DefaultMinVersion = "1.59.0"
	DefaultMaxVersion = "1.60.0"
)

// CLI runs a separately installed LikeC4 executable. Executable defaults
// to LIKEC4_BIN, then to likec4 on PATH. MinVersion and MaxVersion
// override the supported range.
type CLI struct {
	Executable             string
	MinVersion, MaxVersion string
}

func (c CLI) executable() string {
	if c.Executable != "" {
		return c.Executable
	}
	if bin := os.Getenv("LIKEC4_BIN"); bin != "" {
		return bin
	}
	return "likec4"
}

// Supported reports whether a `likec4 --version` string lies within the
// range [MinVersion, MaxVersion) by semver ordering. Pre-releases are
// refused.
func (c CLI) Supported(version string) error {
	minV, maxV := c.MinVersion, c.MaxVersion
	if minV == "" {
		minV = DefaultMinVersion
	}
	if maxV == "" {
		maxV = DefaultMaxVersion
	}
	v := canonicalVersion(version)
	if !semver.IsValid(v) {
		return fmt.Errorf("unsupported LikeC4 version %q: not a semantic version", version)
	}
	if semver.Prerelease(v) != "" {
		return fmt.Errorf("unsupported LikeC4 version %s: pre-release builds are not supported", version)
	}
	lo, hi := canonicalVersion(minV), canonicalVersion(maxV)
	if !semver.IsValid(lo) || !semver.IsValid(hi) {
		return fmt.Errorf("invalid LikeC4 version range [%s, %s)", minV, maxV)
	}
	if semver.Compare(v, lo) < 0 || semver.Compare(v, hi) >= 0 {
		return fmt.Errorf("unsupported LikeC4 version %s; expected >= %s and < %s", version, minV, maxV)
	}
	return nil
}

func canonicalVersion(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// Version runs `likec4 --version` and checks it against the supported
// range. It returns the exact version, for receipts, even when the range
// check fails.
func (c CLI) Version(ctx context.Context) (string, error) {
	b, err := exec.CommandContext(ctx, c.executable(), "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("likec4 --version: %w\n%s", err, b)
	}
	version := strings.TrimSpace(string(b))
	return version, c.Supported(version)
}

// Build checks the version and builds the static site of the workspace in
// dir into out: one self-contained index.html with hash navigation, so
// any static file server can serve it. It returns the exact version.
func (c CLI) Build(ctx context.Context, dir, out string) (string, error) {
	version, err := c.Version(ctx)
	if err != nil {
		return version, err
	}
	args := []string{"build", dir, "--output", out, "--base", "./", "--use-hash-history", "--output-single-file"}
	if b, err := exec.CommandContext(ctx, c.executable(), args...).CombinedOutput(); err != nil {
		return version, fmt.Errorf("likec4 %s: %w\n%s", strings.Join(args, " "), err, b)
	}
	return version, nil
}

package quint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/phoban01/generative-architecture/pkg/model"
)

// VerifyBackend is the model checker behind quint verify. Quint 0.33
// marks temporal support in Apalache as experimental and asks for
// confirmation, so temporal properties go to TLC. TLC needs a Java 21
// runtime.
const VerifyBackend = "tlc"

// VerifyBound describes what a passing verify run covers. TLC has no depth
// bound: it searches the complete reachable state space, so the state
// space must be finite. A model whose state space is too large runs into
// the caller's timeout or the heap limit instead.
const VerifyBound = "complete reachable state space (TLC explicit-state search)"

// VerifyHeap caps the Java heap of every JVM that quint verify starts.
// Quint asks for up to 8 GiB for TLC by default; the cap keeps a large
// state space from exhausting the machine. A run that hits the cap fails
// and leaves the property unchecked.
const VerifyHeap = "2g"

// Outcomes of Verify.
const (
	// Holds: the checker searched the bound and found no violation.
	Holds = "holds"
	// Violated: the checker found a fair behaviour that violates the
	// property.
	Violated = "violated"
)

// Verdict is the result of one completed liveness check.
type Verdict struct {
	Version  string
	Property string
	Outcome  string
	Fairness model.Fairness
	// Detail is the start of the counterexample of a violation.
	Detail string
}

// Verify model-checks one temporal property of m under the fairness fair
// with quint verify. It returns a verdict only when the checker completed;
// any other result (no fairness, a missing Java runtime, an unsupported
// Quint version, a timeout) is an error, so a property is never reported
// as holding without a completed run. The caller owns the timeout.
func Verify(ctx context.Context, m *model.Compiled, t model.Temporal, fair model.Fairness, opts Options) (Verdict, error) {
	v := Verdict{Property: t.Name, Fairness: fair}
	src, err := m.TemporalModule(t, fair)
	if err != nil {
		return v, err
	}
	exe := opts.Executable
	if exe == "" {
		exe = os.Getenv("QUINT_BIN")
	}
	if exe == "" {
		exe = "quint"
	}
	dir, err := os.MkdirTemp("", "architecture-quint-verify-")
	if err != nil {
		return v, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "Model.qnt")
	if err = os.WriteFile(path, []byte(src), 0600); err != nil {
		return v, err
	}
	b, err := exec.CommandContext(ctx, exe, "--version").CombinedOutput()
	if err != nil {
		return v, fmt.Errorf("quint --version: %w\n%s", err, b)
	}
	v.Version = strings.TrimSpace(string(b))
	if err = Supported(v.Version, opts); err != nil {
		return v, err
	}
	cmd := exec.CommandContext(ctx, exe, "verify", path, "--backend", VerifyBackend, "--temporal", t.Name)
	// Apalache writes _apalache-out into the working directory.
	cmd.Dir = dir
	// _JAVA_OPTIONS takes precedence over the options Quint passes.
	cmd.Env = append(os.Environ(), "_JAVA_OPTIONS=-Xmx"+VerifyHeap)
	// On cancellation, stop the Java processes too, not only Quint.
	killGroup(cmd)
	cmd.WaitDelay = 10 * time.Second
	b, err = cmd.CombinedOutput()
	out := string(b)
	if ctx.Err() != nil {
		return v, fmt.Errorf("quint verify did not finish: %w", ctx.Err())
	}
	var exit *exec.ExitError
	switch {
	case err == nil && strings.Contains(out, "[ok]"):
		v.Outcome = Holds
		return v, nil
	case errors.As(err, &exit) && strings.Contains(out, "[violation]"):
		v.Outcome = Violated
		v.Detail = counterexample(out)
		return v, nil
	case err == nil:
		return v, fmt.Errorf("quint verify gave no verdict:\n%s", tail(out, 6))
	default:
		return v, fmt.Errorf("quint verify: %w\n%s", err, tail(out, 6))
	}
}

// counterexample keeps the states of TLC's counterexample, one line each.
func counterexample(out string) string {
	var events []string
	last := ""
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "State ") {
			continue
		}
		if strings.Contains(line, "Stuttering") {
			events = append(events, "stuttering")
			continue
		}
		event := "?"
		var state []string
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(next), "/\\"))
			if next == "" || strings.HasPrefix(next, "State ") {
				break
			}
			if v, ok := strings.CutPrefix(next, "event = "); ok {
				event = strings.Trim(v, `"`)
			}
			state = append(state, next)
		}
		events = append(events, event)
		last = strings.Join(state, ", ")
	}
	if len(events) == 0 {
		return tail(out, 6)
	}
	detail := fmt.Sprintf("%d states; events: %s; last state: %s", len(events), strings.Join(events, " -> "), last)
	if len(detail) > maxDetail {
		detail = detail[:maxDetail] + " ..."
	}
	return detail
}

// maxDetail bounds the counterexample summary in a report finding.
const maxDetail = 1500

// tail keeps the last n non-empty lines of a tool's output.
func tail(out string, n int) string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

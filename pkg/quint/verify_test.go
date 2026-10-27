package quint_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/quint"
)

// counterModel counts from 0 to 3. Nothing forces it to count: without a
// fairness assumption it may stutter at 0 forever.
func counterModel(t *testing.T) *model.Compiled {
	t.Helper()
	m, err := model.Compile(model.Spec{
		Name:       "Counter",
		State:      map[string]model.Variable{"count": {Type: "int", Initial: "0"}},
		Actions:    []model.Action{{Name: "increment", Guard: "count < 3", Updates: map[string]expr.Expression{"count": "count + 1"}}},
		Properties: []model.Property{{Name: "bounded", Expression: "count <= 3"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// verifier returns the Quint CLI for a liveness check, or skips the test:
// quint verify needs QUINT_BIN and a Java runtime for TLC.
func verifier(t *testing.T) string {
	t.Helper()
	exe := os.Getenv("QUINT_BIN")
	if exe == "" {
		t.Skip("QUINT_BIN unset")
	}
	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("java not on PATH: quint verify with TLC needs a Java 21 runtime")
	}
	return exe
}

func verify(t *testing.T, temporal model.Temporal) quint.Verdict {
	t.Helper()
	exe := verifier(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fair := model.Fairness{Weak: []string{"increment"}}
	v, err := quint.Verify(ctx, counterModel(t), temporal, fair, quint.Options{Executable: exe})
	if err != nil {
		t.Fatal(err)
	}
	if v.Property != temporal.Name || v.Fairness.String() != "weak(increment)" || v.Version == "" {
		t.Fatalf("%+v", v)
	}
	return v
}

func TestVerifyHolds(t *testing.T) {
	v := verify(t, model.Temporal{Name: "reachesThree", Eventually: "count == 3"})
	if v.Outcome != quint.Holds || v.Detail != "" {
		t.Fatalf("%+v", v)
	}
}

func TestVerifyViolated(t *testing.T) {
	v := verify(t, model.Temporal{Name: "oneLeadsToZero", LeadsTo: &model.LeadsTo{From: "count == 1", To: "count == 0"}})
	if v.Outcome != quint.Violated || !strings.Contains(v.Detail, "events: init -> increment") || !strings.Contains(v.Detail, "count = ") {
		t.Fatalf("%+v", v)
	}
}

// TestVerifyNeedsFairness never runs the checker: a property without a
// fairness assumption is an error, so the caller reports it as unchecked.
func TestVerifyNeedsFairness(t *testing.T) {
	v, err := quint.Verify(context.Background(), counterModel(t), model.Temporal{Name: "reachesThree", Eventually: "count == 3"}, model.Fairness{}, quint.Options{Executable: "/nonexistent/quint"})
	if !errors.Is(err, model.ErrNoFairness) || v.Outcome != "" {
		t.Fatalf("got %+v, %v", v, err)
	}
}

// TestVerifyWithoutChecker uses a fake Quint CLI whose verify command
// fails the way it does without Java. There is no verdict, only an error.
func TestVerifyWithoutChecker(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "quint")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.33.0; exit 0; fi\necho 'apalache-mc: line 83: exec: java: not found' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	v, err := quint.Verify(context.Background(), counterModel(t), model.Temporal{Name: "reachesThree", Eventually: "count == 3"}, model.Fairness{Weak: []string{"increment"}}, quint.Options{Executable: fake})
	if err == nil || v.Outcome != "" || !strings.Contains(err.Error(), "java: not found") {
		t.Fatalf("got %+v, %v", v, err)
	}
	// Output that claims success without the [ok] marker is no verdict.
	script = "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.33.0; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if v, err = quint.Verify(context.Background(), counterModel(t), model.Temporal{Name: "reachesThree", Eventually: "count == 3"}, model.Fairness{Weak: []string{"increment"}}, quint.Options{Executable: fake}); err == nil || v.Outcome != "" {
		t.Fatalf("got %+v, %v", v, err)
	}
}

// TestVerifyTimeout uses a fake Quint CLI whose verify command starts a
// child process that outlives it, as the TLC JVM does. The timeout must
// stop both and return an error, not a verdict. The fake records the heap
// cap it was given.
func TestVerifyTimeout(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "quint")
	heap := filepath.Join(dir, "heap")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.33.0; exit 0; fi\necho \"$_JAVA_OPTIONS\" > " + heap + "\npwd > " + heap + ".cwd\nsleep 300 &\nwait\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	v, err := quint.Verify(ctx, counterModel(t), model.Temporal{Name: "reachesThree", Eventually: "count == 3"}, model.Fairness{Weak: []string{"increment"}}, quint.Options{Executable: fake})
	if err == nil || v.Outcome != "" || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("got %+v, %v", v, err)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("Verify returned after %s; the child process kept it waiting", elapsed)
	}
	if b, err := os.ReadFile(heap); err != nil || strings.TrimSpace(string(b)) != "-Xmx"+quint.VerifyHeap {
		t.Fatalf("heap cap %q, %v", b, err)
	}
	// Apalache output must not land in the caller's directory.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(heap + ".cwd"); err != nil || strings.TrimSpace(string(b)) == wd {
		t.Fatalf("quint verify ran in %q, %v", b, err)
	}
}

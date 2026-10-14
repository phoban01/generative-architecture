package conformance_test

import (
	"context"
	"errors"
	"fmt"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/model"
	"strings"
	"testing"
)

// counterModel admits five increments but only promises three, so a valid
// trace can reach a state that violates the property.
func counterModel(t *testing.T) *model.Compiled {
	t.Helper()
	m, err := model.Compile(model.Spec{Name: "Counter", State: map[string]model.Variable{"count": {Type: "int", Initial: "0"}}, Actions: []model.Action{{Name: "increment", Guard: "count < 5", Updates: map[string]expr.Expression{"count": "count + 1"}}}, Properties: []model.Property{{Name: "bounded", Expression: "count <= 3"}}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type counter struct {
	value   int64
	mutant  bool
	failAt  int64
	failErr error
}

func (c *counter) Apply(_ context.Context, event string) error {
	if c.failErr != nil && c.value == c.failAt {
		return c.failErr
	}
	c.value++
	if c.mutant {
		c.value++
	}
	return nil
}
func (c *counter) Project(context.Context) (map[string]any, error) {
	return map[string]any{"count": c.value}, nil
}

func requireFailure(t *testing.T, err error, category conformance.Category, step int, event, message string) *conformance.Failure {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s failure", category)
	}
	f, ok := conformance.AsFailure(err)
	if !ok {
		t.Fatalf("expected *Failure, got %T: %v", err, err)
	}
	if f.Category != category || f.Step != step || f.Event != event {
		t.Fatalf("got %s at step %d (%s), want %s at step %d (%s): %v", f.Category, f.Step, f.Event, category, step, event, err)
	}
	if conformance.CategoryOf(err) != category {
		t.Fatalf("CategoryOf(%v) = %s", err, conformance.CategoryOf(err))
	}
	if !strings.Contains(err.Error(), message) || !strings.HasPrefix(err.Error(), string(category)+": ") {
		t.Fatalf("message %q lacks %q or the category prefix", err.Error(), message)
	}
	return f
}

func TestReplayFailureCategories(t *testing.T) {
	m := counterModel(t)
	ctx := context.Background()
	ok, err := conformance.Scenario(m, "increment", "increment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conformance.Replay(ctx, m, ok, &counter{}); err != nil {
		t.Fatal(err)
	}
	t.Run("safety", func(t *testing.T) {
		violating, err := conformance.Scenario(m, "increment", "increment", "increment", "increment")
		if err != nil {
			t.Fatal(err)
		}
		_, err = conformance.Replay(ctx, m, violating, &counter{})
		f := requireFailure(t, err, conformance.CategorySafety, 4, "increment", "property bounded violated")
		if !strings.Contains(f.Err.Error(), "step 4") {
			t.Fatal(f.Err)
		}
	})
	t.Run("conformance projection", func(t *testing.T) {
		_, err := conformance.Replay(ctx, m, ok, &counter{mutant: true})
		requireFailure(t, err, conformance.CategoryConformance, 1, "increment", "conformance mismatch at step 1")
	})
	t.Run("conformance trace", func(t *testing.T) {
		altered := conformance.Trace{Status: ok.Status, States: append([]conformance.State(nil), ok.States...)}
		altered.States[1] = conformance.State{Event: "increment", Values: map[string]any{"count": int64(10)}}
		_, err := conformance.Replay(ctx, m, altered, &counter{})
		requireFailure(t, err, conformance.CategoryConformance, 1, "increment", "CEL/trace mismatch")
	})
	t.Run("conformance disabled action", func(t *testing.T) {
		altered := conformance.Trace{Status: ok.Status, States: append([]conformance.State(nil), ok.States...)}
		altered.States[2].Event = "decrement"
		_, err := conformance.Replay(ctx, m, altered, &counter{})
		requireFailure(t, err, conformance.CategoryConformance, 2, "decrement", "model step 2")
	})
	t.Run("unsupported", func(t *testing.T) {
		d := &goflow.Diagnostic{File: "controller.go", Line: 7, Construct: "loop", Reason: "unsupported loop"}
		_, err := conformance.Replay(ctx, m, ok, &counter{failAt: 1, failErr: fmt.Errorf("extract: %w", d)})
		f := requireFailure(t, err, conformance.CategoryUnsupported, 2, "increment", "controller.go:7: unsupported loop")
		if got, ok := goflow.AsDiagnostic(f); !ok || got != d {
			t.Fatal("diagnostic not preserved through the failure")
		}
	})
	t.Run("tool adapter", func(t *testing.T) {
		boom := errors.New("fixture exploded")
		_, err := conformance.Replay(ctx, m, ok, &counter{failAt: 0, failErr: boom})
		requireFailure(t, err, conformance.CategoryTool, 1, "increment", "adapter step 1 (increment): fixture exploded")
		if !errors.Is(err, boom) {
			t.Fatal("cause not wrapped")
		}
	})
	t.Run("tool input", func(t *testing.T) {
		_, err := conformance.Replay(ctx, m, conformance.Trace{Status: "violation", States: ok.States}, &counter{})
		requireFailure(t, err, conformance.CategoryTool, 0, "", "expected an ok trace")
		_, err = conformance.ReplayObserved(ctx, m, ok, &counter{}, []string{"missing"})
		requireFailure(t, err, conformance.CategoryTool, 0, "init", "unknown observation field missing")
		_, err = conformance.ReplayObserved(ctx, m, ok, &counter{}, nil)
		requireFailure(t, err, conformance.CategoryTool, 0, "", "observable fields required")
	})
	t.Run("tool cancellation", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := conformance.Replay(cancelled, m, ok, &counter{})
		requireFailure(t, err, conformance.CategoryTool, 0, "init", "context canceled")
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestCategoryOf(t *testing.T) {
	d := &goflow.Diagnostic{Construct: "goto", Reason: "unsupported goto"}
	wrapped := fmt.Errorf("outer: %w", &conformance.Failure{Category: conformance.CategorySafety, Step: 3, Event: "x", Err: errors.New("property p violated")})
	for name, c := range map[string]struct {
		err  error
		want conformance.Category
	}{
		"nil":                {nil, ""},
		"plain":              {errors.New("boom"), conformance.CategoryTool},
		"diagnostic":         {d, conformance.CategoryUnsupported},
		"wrapped diagnostic": {fmt.Errorf("adapter: %w", d), conformance.CategoryUnsupported},
		"wrapped failure":    {wrapped, conformance.CategorySafety},
		"failure":            {&conformance.Failure{Category: conformance.CategoryConformance}, conformance.CategoryConformance},
	} {
		if got := conformance.CategoryOf(c.err); got != c.want {
			t.Errorf("%s: CategoryOf = %q, want %q", name, got, c.want)
		}
	}
	if _, ok := conformance.AsFailure(errors.New("boom")); ok {
		t.Fatal("plain error reported as Failure")
	}
	f := &conformance.Failure{Category: conformance.CategoryTool}
	if f.Error() != "tool failure" {
		t.Fatal(f.Error())
	}
}

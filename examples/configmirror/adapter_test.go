package configmirror

import (
	"context"
	"generative-architecture/pkg/conformance"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var scenarios = map[string][]string{
	"copy":              {"enqueue", "begin", "readSource", "readTarget", "commit", "finish"},
	"stale-cache":       {"changeSource", "enqueue", "begin", "readSource", "deliverCache", "readTarget", "commit", "finish"},
	"retained-snapshot": {"enqueue", "begin", "readSource", "changeSource", "deliverCache", "readTarget", "commit", "finish"},
	"dirty":             {"enqueue", "begin", "enqueue", "enqueue", "readSource", "readTarget", "commit", "finish", "begin", "readSource", "readTarget", "commit", "finish"},
	"retry":             {"enqueue", "begin", "readSource", "readTarget", "writeError", "finish", "advance", "advance", "fire", "begin", "readSource", "readTarget", "commit", "finish"},
	"source-error":      {"enqueue", "begin", "readSourceError", "finish"},
	"target-error":      {"enqueue", "begin", "readSource", "readTargetError", "finish"},
}

func TestScenarios(t *testing.T) {
	m, err := Load("system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for name, steps := range scenarios {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, err := New(ctx, m.Effects())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			trace, err := conformance.Scenario(m, steps...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = conformance.Replay(ctx, m, trace, a); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestQuintTraces(t *testing.T) {
	m, err := Load("system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob("traces/*.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 40 {
		t.Fatalf("expected 40 recorded Quint traces; run make library-demo; got %d", len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, err := New(ctx, m.Effects())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			trace, err := conformance.ReadITF(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = conformance.Replay(ctx, m, trace, a); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMutationsDetected(t *testing.T) {
	m, err := Load("system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for mutation, scenario := range map[string]string{"direct-source": "stale-cache", "wrong-write": "copy"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, err := newAdapter(ctx, m.Effects(), mutation)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			trace, err := conformance.Scenario(m, scenarios[scenario]...)
			if err != nil {
				t.Fatal(err)
			}
			_, err = conformance.Replay(ctx, m, trace, a)
			if err == nil || !strings.Contains(err.Error(), "conformance mismatch") {
				t.Fatalf("mutation escaped or failed for unrelated reason: %v", err)
			}
		})
	}
}
func TestCancelAtEveryBoundary(t *testing.T) {
	m, err := Load("system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, steps := range [][]string{{"enqueue", "begin"}, {"enqueue", "begin", "readSource"}, {"enqueue", "begin", "readSource", "readTarget"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		a, err := New(ctx, m.Effects())
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		for _, step := range steps {
			if err = a.Apply(ctx, step); err != nil {
				a.Close()
				cancel()
				t.Fatal(err)
			}
		}
		err = a.Close()
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
}

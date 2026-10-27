package generated

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/fixture"
	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ RuntimeWorker = (*cr.Worker)(nil)

func TestRuntimeUpToDate(t *testing.T) {
	m, err := fixture.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateRuntime(fixture.Package, m, fixture.RuntimeBinding())
	if err != nil {
		t.Fatal(err)
	}
	assertGenerated(t, "runtime.go", src)
}

// The generated routing drives a real controller-runtime worker through a
// failing and a succeeding invocation and a retry timer, and the generated
// projection observes its queue and clock after every step.
func TestRuntimeDrivesWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failures := 1
	w, err := cr.NewWorker(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "demo", Name: "db-1"}}, 2, reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
		if failures > 0 {
			failures--
			return reconcile.Result{}, errors.New("transient")
		}
		return reconcile.Result{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	state := func(ready, dirty, processing bool, retries, now, due int64) map[string]any {
		return map[string]any{"ready": ready, "dirty": dirty, "processing": processing, "retries": retries, "now": now, "due": due}
	}
	for i, step := range []struct {
		event string
		want  map[string]any
	}{
		{"enqueue", state(true, true, false, 0, 0, -1)},
		{"begin", state(false, false, true, 0, 0, -1)},
		{"finishError", state(false, false, false, 1, 0, 2)},
		{"advance", state(false, false, false, 1, 1, 2)},
		{"advance", state(false, false, false, 1, 2, 2)},
		{"fire", state(true, true, false, 1, 2, -1)},
		{"begin", state(false, false, true, 1, 2, -1)},
		{"finish", state(false, false, false, 0, 2, -1)},
	} {
		ok, err := ApplyRuntime(w, step.event)
		if !ok || err != nil {
			t.Fatalf("step %d %s: routed %t, %v", i, step.event, ok, err)
		}
		if got := ProjectRuntime(w); !reflect.DeepEqual(got, step.want) {
			t.Fatalf("step %d %s: projected %v, want %v", i, step.event, got, step.want)
		}
	}
	if ok, err := ApplyRuntime(w, "node1"); ok || err != nil {
		t.Fatalf("a process event is not a runtime event: %t, %v", ok, err)
	}
	if _, err := ApplyRuntime(w, "finish"); err == nil {
		t.Fatal("finishing without an invocation must fail")
	}
}

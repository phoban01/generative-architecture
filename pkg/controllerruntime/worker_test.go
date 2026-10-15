package controllerruntime_test

import (
	"context"
	"testing"
	"time"

	cr "generative-architecture/pkg/controllerruntime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The worker reports the runtime profile's dirty and processing flags: an
// add marks the key dirty, a start takes it, an add during the invocation
// keeps it dirty but not ready, and waiting ends processing and makes the
// key ready again.
func TestWorkerDirtyAndProcessing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "demo", Name: "a"}}
	w, err := cr.NewWorker(ctx, key, 1, reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
		return reconcile.Result{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	check := func(step string, ready, dirty, processing bool) {
		t.Helper()
		if w.Ready() != ready || w.Dirty() != dirty || w.Processing() != processing {
			t.Fatalf("%s: ready=%v dirty=%v processing=%v, want %v %v %v", step, w.Ready(), w.Dirty(), w.Processing(), ready, dirty, processing)
		}
	}
	check("initial", false, false, false)
	w.Enqueue()
	check("enqueue", true, true, false)
	if err := w.StartOne(); err != nil {
		t.Fatal(err)
	}
	check("start", false, false, true)
	w.Enqueue()
	check("enqueue while processing", false, true, true)
	if err := w.Wait(); err != nil {
		t.Fatal(err)
	}
	check("finish", true, true, false)
}

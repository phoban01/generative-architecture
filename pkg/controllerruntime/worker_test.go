package controllerruntime_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"

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

// On a priority queue with startup and deletion, the worker mirrors the
// item's priority as the queue summary merges it: the initial list adds at
// LowPriority, a normal add raises it, an invocation is taken at the item's
// priority and a failed one is re-added at that priority, and a tombstone
// enqueues the key once and marks it deleted.
func TestWorkerPriorityStartupDeletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key := reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "demo", Name: "a"}}
	fail := true
	w, err := cr.NewWorkerWith(ctx, key, cr.WorkerOptions{Queue: cr.QueuePriority, Startup: true, Deletion: true, RetryTicks: 1}, reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
		if fail {
			return reconcile.Result{}, fmt.Errorf("transient")
		}
		return reconcile.Result{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.Synced() {
		t.Fatal("a startup worker starts synced")
	}
	w.EnqueueLow()
	if !w.Queued() || w.Priority() != cr.LowPriority {
		t.Fatalf("after the initial list: queued=%v priority=%d", w.Queued(), w.Priority())
	}
	if err := w.Sync(); err != nil || !w.Synced() {
		t.Fatalf("sync: %v synced=%v", err, w.Synced())
	}
	if err := w.StartOne(); err != nil {
		t.Fatal(err)
	}
	if w.Taken() != cr.LowPriority || w.Queued() {
		t.Fatalf("start: taken=%d queued=%v", w.Taken(), w.Queued())
	}
	if err := w.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, due := w.Clock(); due < 0 || w.Priority() != cr.LowPriority || w.Retries() != 1 {
		t.Fatalf("after an error: due=%d priority=%d retries=%d", due, w.Priority(), w.Retries())
	}
	w.Enqueue()
	if !w.Queued() || w.Priority() != 0 {
		t.Fatalf("a normal add did not raise the priority: queued=%v priority=%d", w.Queued(), w.Priority())
	}
	fail = false
	if err := w.StartOne(); err != nil {
		t.Fatal(err)
	}
	if w.Taken() != 0 {
		t.Fatalf("taken %d, want 0", w.Taken())
	}
	if err := w.Wait(); err != nil {
		t.Fatal(err)
	}
	if w.Queued() || w.Retries() != 0 {
		t.Fatalf("after success: queued=%v retries=%d", w.Queued(), w.Retries())
	}
	if err := w.Tombstone(); err != nil || !w.Deleted() || !w.Queued() {
		t.Fatalf("tombstone: %v deleted=%v queued=%v", err, w.Deleted(), w.Queued())
	}
	if err := w.Tombstone(); err == nil {
		t.Fatal("a second tombstone must fail")
	}
}

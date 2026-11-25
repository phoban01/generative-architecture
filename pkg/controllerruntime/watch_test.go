package controllerruntime_test

import (
	"context"
	"testing"
	"time"

	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestNativeEventHandlerUsesDeclaredQueueDomain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	key := reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "demo", Name: "infra-1"}}
	w, err := cr.NewWorker(ctx, key, 1, reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
		return reconcile.Result{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	h := &handler.EnqueueRequestForObject{}
	deliver := func(name string) ([]reconcile.Request, error) {
		return w.EnqueueFrom(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			h.Create(ctx, event.CreateEvent{Object: &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: name}}}, q)
		})
	}
	if keys, err := deliver("foreign"); err == nil || len(keys) != 0 || w.Ready() {
		t.Fatalf("undeclared key entered queue: %v %v", keys, err)
	}
	for i := 0; i < 2; i++ {
		keys, err := deliver(key.Name)
		if err != nil || len(keys) != 1 || keys[0] != key {
			t.Fatalf("handler did not submit expected key: %v %v", keys, err)
		}
	}
	if !w.Ready() {
		t.Fatal("duplicate notifications must leave exactly one ready key")
	}
	if err := w.StartOne(); err != nil {
		t.Fatal(err)
	}
	if err := w.Wait(); err != nil {
		t.Fatal(err)
	}
	if w.Ready() {
		t.Fatal("duplicate notification caused a second invocation")
	}
}

// On a priority-queue controller the handler sink is a PriorityQueue, so an
// initial-list event delivered through a native handler comes out at
// LowPriority and a later event at the default priority.
func TestNativeHandlerCarriesLowPriority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g := cr.NewGroup(ctx)
	defer g.Close()
	key := reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "demo", Name: "a"}}
	c, err := g.Add(cr.ControllerOptions{Name: "handler", Queue: cr.QueuePriority, Startup: true, Workers: 1, Keys: []reconcile.Request{key}, RetryTicks: 1, Reconciler: reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
		return reconcile.Result{}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	h := &handler.EnqueueRequestForObject{}
	object := &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "a"}}
	deliver := func(initial bool) {
		keys, err := c.EnqueueFrom(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			h.Create(ctx, event.CreateEvent{Object: object, IsInInitialList: initial}, q)
		})
		if err != nil || len(keys) != 1 || keys[0] != key {
			t.Fatalf("handler did not submit the key: %v %v", keys, err)
		}
	}
	deliver(true)
	if err := c.Sync(); err != nil {
		t.Fatal(err)
	}
	inv, err := c.Start()
	if err != nil {
		t.Fatal(err)
	}
	if inv.Priority != cr.LowPriority {
		t.Fatalf("initial-list event handed out at priority %d, want %d", inv.Priority, cr.LowPriority)
	}
	if _, _, err := inv.Finish(); err != nil {
		t.Fatal(err)
	}
	deliver(false)
	if inv, err = c.Start(); err != nil {
		t.Fatal(err)
	}
	if inv.Priority != 0 {
		t.Fatalf("event handed out at priority %d, want 0", inv.Priority)
	}
	if _, _, err := inv.Finish(); err != nil {
		t.Fatal(err)
	}
}

package controllerruntime

import (
	"context"
	"fmt"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Worker is the one-key convenience facade. Group supports multiple controllers,
// keys and workers with the same real queue and invocation implementation.
type Worker struct {
	group      *Group
	controller *Controller
	key        reconcile.Request
	invocation *Invocation
	// dirty mirrors the work queue's dirty set for the key, which the queue
	// does not expose: set when the key is added, cleared when a worker
	// takes it. An add during an invocation stays dirty until the worker
	// is done and the queue re-adds the key.
	dirty bool
}

func NewWorker(ctx context.Context, key reconcile.Request, retryTicks int, r reconcile.Reconciler) (*Worker, error) {
	g := NewGroup(ctx)
	c, err := g.Add(ControllerOptions{Name: "architecture-conformance", Workers: 1, Keys: []reconcile.Request{key}, RetryTicks: retryTicks, Reconciler: r})
	if err != nil {
		g.Close()
		return nil, err
	}
	return &Worker{group: g, controller: c, key: key}, nil
}
func (w *Worker) Enqueue() {
	if w.controller.Enqueue(w.key) == nil {
		w.dirty = true
	}
}
func (w *Worker) Ready() bool { return w.controller.Ready() == 1 }

// Dirty reports whether the key was added and not yet taken by a worker:
// the runtime profile's dirty flag.
func (w *Worker) Dirty() bool { return w.dirty }

// Processing reports whether an invocation started and was not yet waited
// for: the runtime profile's processing flag.
func (w *Worker) Processing() bool { return w.invocation != nil }
func (w *Worker) Retries() int64   { return w.controller.Retries(w.key) }
func (w *Worker) StartOne() error {
	if w.invocation != nil {
		return fmt.Errorf("invocation already active")
	}
	inv, err := w.controller.Start()
	if err != nil {
		return err
	}
	w.invocation = inv
	w.dirty = false
	return nil
}
func (w *Worker) Wait() error {
	if w.invocation == nil {
		return fmt.Errorf("no active invocation")
	}
	_, _, err := w.invocation.Finish()
	if err == nil {
		w.invocation = nil
	}
	return err
}
func (w *Worker) Clock() (int64, int64) { return w.group.Now(), w.controller.Due(w.key) }
func (w *Worker) Advance()              { w.group.Advance() }
func (w *Worker) Fire() error {
	if err := w.controller.Fire(w.key); err != nil {
		return err
	}
	w.dirty = true
	return nil
}
func (w *Worker) Close() error { return w.group.Close() }

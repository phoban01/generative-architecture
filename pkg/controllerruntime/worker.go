package controllerruntime

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// WorkerOptions configure the one-key worker: the native queue kind, and
// whether the controller models its startup (the initial list and the
// cache sync) and deletion (tombstones).
type WorkerOptions struct {
	Queue      string
	Startup    bool
	Deletion   bool
	RetryTicks int
}

// Worker is the one-key convenience facade. Group supports multiple controllers,
// keys and workers with the same real queue and invocation implementation.
// It mirrors the per-key state of the runtime profile that the native queue
// does not expose: the FIFO dirty flag, and for a priority queue the item's
// priority and the priority its invocation was taken with, with the merge
// rule of the priority queue summary (see addPriorityController).
type Worker struct {
	group      *Group
	controller *Controller
	key        reconcile.Request
	options    WorkerOptions
	invocation *Invocation
	// dirty mirrors the work queue's dirty set for the key, which the queue
	// does not expose: set when the key is added, cleared when a worker
	// takes it. An add during an invocation stays dirty until the worker
	// is done and the queue re-adds the key.
	dirty bool
	// priority and taken mirror the priority queue's item priority and the
	// priority of the running invocation.
	priority, taken int64
	deleted         bool
}

// NewWorker starts a FIFO worker without startup or deletion.
func NewWorker(ctx context.Context, key reconcile.Request, retryTicks int, r reconcile.Reconciler) (*Worker, error) {
	return NewWorkerWith(ctx, key, WorkerOptions{Queue: QueueFIFO, RetryTicks: retryTicks}, r)
}

// NewWorkerWith starts a worker with options.
func NewWorkerWith(ctx context.Context, key reconcile.Request, o WorkerOptions, r reconcile.Reconciler) (*Worker, error) {
	g := NewGroup(ctx)
	c, err := g.Add(ControllerOptions{Name: "architecture-conformance", Queue: o.Queue, Startup: o.Startup, Workers: 1, Keys: []reconcile.Request{key}, RetryTicks: o.RetryTicks, Reconciler: r})
	if err != nil {
		g.Close()
		return nil, err
	}
	return &Worker{group: g, controller: c, key: key, options: o}, nil
}

func (w *Worker) priorityQueue() bool { return w.controller.pq != nil }

// present reports whether the priority queue holds an item for the key,
// ready or waiting.
func (w *Worker) present() bool { return w.controller.Queued(w.key) || w.controller.Due(w.key) >= 0 }

// merge records an add at priority p: the item keeps the higher priority.
func (w *Worker) merge(present bool, p int64) {
	if !present || p > w.priority {
		w.priority = p
	}
}

// Enqueue adds the key as a watch event does, at normal priority.
func (w *Worker) Enqueue() {
	present := w.present()
	if w.controller.Enqueue(w.key) == nil {
		w.dirty = true
		if w.priorityQueue() {
			w.merge(present, 0)
		}
	}
}

// EnqueueLow adds the key as the initial list does: at LowPriority on a
// priority queue, as a plain add on a FIFO queue.
func (w *Worker) EnqueueLow() {
	present := w.present()
	if w.controller.EnqueueLow(w.key) == nil {
		w.dirty = true
		if w.priorityQueue() {
			w.merge(present, LowPriority)
		}
	}
}

// Tombstone enqueues the key for a delete the informer missed and marks
// it deleted; a key is deleted at most once.
func (w *Worker) Tombstone() error {
	if !w.options.Deletion {
		return fmt.Errorf("the worker does not model deletion")
	}
	if w.deleted {
		return fmt.Errorf("the key is already deleted")
	}
	w.deleted = true
	w.Enqueue()
	return nil
}

// Sync marks the controller's caches synced, so its workers start.
func (w *Worker) Sync() error { return w.controller.Sync() }

// Synced reports whether the controller's caches synced; a worker without
// startup counts as synced.
func (w *Worker) Synced() bool { return w.controller.Synced() }

// Deleted reports whether the key was tombstoned.
func (w *Worker) Deleted() bool { return w.deleted }

// Ready reports whether the queue holds a ready item.
func (w *Worker) Ready() bool { return w.controller.Ready() == 1 }

// Queued reports whether the priority queue holds a ready item for the
// key; it is false for a FIFO queue.
func (w *Worker) Queued() bool { return w.controller.Queued(w.key) }

// Priority is the priority of the key's item in the priority queue.
func (w *Worker) Priority() int64 { return w.priority }

// Taken is the priority the running or last invocation was taken with.
func (w *Worker) Taken() int64 { return w.taken }

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
	if w.priorityQueue() && int64(inv.Priority) != w.priority {
		return fmt.Errorf("priority queue handed out the key at priority %d, expected %d", inv.Priority, w.priority)
	}
	w.invocation = inv
	w.dirty = false
	w.taken = w.priority
	return nil
}

// Wait finishes the invocation. On a priority queue, an outcome the handler
// re-adds (an error, a requeue or a requeue after a delay) merges the key
// at the priority it was taken with.
func (w *Worker) Wait() error {
	if w.invocation == nil {
		return fmt.Errorf("no active invocation")
	}
	present, retries := w.present(), w.Retries()
	result, rerr, err := w.invocation.Finish()
	if err != nil {
		return err
	}
	w.invocation = nil
	if w.priorityQueue() && (result.Requeue || result.RequeueAfter > 0 || (rerr != nil && w.Retries() > retries)) {
		w.merge(present, w.taken)
	}
	return nil
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

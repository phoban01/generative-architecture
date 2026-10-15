package controllerruntime

import (
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// EnqueueFrom delivers a native event handler to this controller's real queue.
// It returns submitted keys so observation adapters can track notification
// obligations. Keys outside the declared domain fail instead of starting an
// unmodelled invocation. Delivery is synchronous on the scheduler's goroutine.
func (c *Controller) EnqueueFrom(deliver func(workqueue.TypedRateLimitingInterface[reconcile.Request])) ([]reconcile.Request, error) {
	q := &eventSink{TypedRateLimitingInterface: c.queue, controller: c}
	deliver(q)
	return q.keys, q.err
}

type eventSink struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	controller *Controller
	keys       []reconcile.Request
	err        error
}

func (q *eventSink) Add(key reconcile.Request) {
	if err := q.controller.Enqueue(key); err != nil {
		q.err = err
		return
	}
	q.keys = append(q.keys, key)
}

// EnqueueFrom is the one-key facade for native event-handler delivery.
func (w *Worker) EnqueueFrom(deliver func(workqueue.TypedRateLimitingInterface[reconcile.Request])) ([]reconcile.Request, error) {
	keys, err := w.controller.EnqueueFrom(deliver)
	if len(keys) > 0 {
		w.dirty = true
	}
	return keys, err
}

package controllerruntime

import (
	"errors"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// EnqueueFrom delivers a native event handler to this controller's real queue.
// It returns submitted keys so observation adapters can track notification
// obligations. Keys outside the declared domain fail instead of starting an
// unmodelled invocation. Delivery is synchronous on the scheduler's goroutine.
//
// On a priority-queue controller the sink is a priorityqueue.PriorityQueue,
// so controller-runtime's handlers take their priority path and an
// initial-list event arrives at LowPriority, as it does in a real manager.
func (c *Controller) EnqueueFrom(deliver func(workqueue.TypedRateLimitingInterface[reconcile.Request])) ([]reconcile.Request, error) {
	sink := &eventSink{TypedRateLimitingInterface: c.queue, controller: c}
	if c.pq != nil {
		deliver(&prioritySink{eventSink: sink})
	} else {
		deliver(sink)
	}
	return sink.keys, sink.err
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

// prioritySink is the sink of a priority-queue controller. A handler that
// sees a PriorityQueue adds with options; a LowPriority add is an
// initial-list event and goes through EnqueueLow, every other add through
// Enqueue. Delays and rate limits are not handler paths and fail closed.
type prioritySink struct {
	*eventSink
}

func (q *prioritySink) AddWithOpts(o priorityqueue.AddOpts, keys ...reconcile.Request) {
	for _, key := range keys {
		var err error
		switch {
		case o.After > 0 || o.RateLimited:
			err = errSinkOptions
		case o.Priority != nil && *o.Priority == LowPriority:
			err = q.controller.EnqueueLow(key)
		default:
			err = q.controller.Enqueue(key)
		}
		if err != nil {
			q.err = err
			return
		}
		q.keys = append(q.keys, key)
	}
}

// GetWithPriority is not a handler path; the scheduler takes keys itself.
func (q *prioritySink) GetWithPriority() (reconcile.Request, int, bool) {
	return q.controller.pq.GetWithPriority()
}

// EnqueueFrom is the one-key facade for native event-handler delivery.
func (w *Worker) EnqueueFrom(deliver func(workqueue.TypedRateLimitingInterface[reconcile.Request])) ([]reconcile.Request, error) {
	keys, err := w.controller.EnqueueFrom(deliver)
	if len(keys) > 0 {
		w.dirty = true
	}
	return keys, err
}

// errSinkOptions reports an add with a delay or a rate limit through the
// handler sink, which controller-runtime's handlers never do.
var errSinkOptions = errors.New("event handler added with a delay or rate limit")

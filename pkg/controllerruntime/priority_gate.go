package controllerruntime

import (
	"fmt"
	"math"
	"sync"
	"time"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// parked is the wall-clock delay of every delayed add in the native priority
// queue. The queue keeps its own clock, so the gate parks delayed items far in
// the future and moves them by the group's logical clock instead.
const parked = 24 * 365 * time.Hour

// priorityGate runs controller-runtime's native priority queue under the
// group's driver. The native queue decides deduplication, priorities,
// counters and the order of Get. The gate holds Get until the driver grants a
// permit, and it replaces wall-clock timers with logical ones:
//
//   - A delayed or rate-limited add reaches the native queue with a parked
//     ready time. The native queue still merges it with the existing item, so
//     the counter and priority stay native. The gate records the logical due
//     tick the native queue would use.
//   - Fire sets a ready time of one nanosecond. The native timer goroutine
//     then makes the item ready with a new counter, as a real timer does.
//
// The gate mirrors which keys have a ready or waiting item, using the rules
// of the native queue, so the driver can read due ticks and know when Get
// will not block.
type priorityGate struct {
	priorityqueue.PriorityQueue[reconcile.Request]
	owner      *Controller
	retryTicks int64
	mu         sync.Mutex
	items      map[reconcile.Request]int64 // -1 ready, otherwise the due tick
	priorities map[reconcile.Request]int   // priority each key was last taken with
}

func newPriorityGate(owner *Controller, retryTicks int) *priorityGate {
	limiter := workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](parked, parked)
	// An empty name disables the queue's metrics.
	native := priorityqueue.New[reconcile.Request]("", func(o *priorityqueue.Opts[reconcile.Request]) { o.RateLimiter = limiter })
	return &priorityGate{PriorityQueue: native, owner: owner, retryTicks: int64(retryTicks), items: map[reconcile.Request]int64{}, priorities: map[reconcile.Request]int{}}
}

func (q *priorityGate) Add(key reconcile.Request) { q.AddWithOpts(priorityqueue.AddOpts{}, key) }
func (q *priorityGate) AddAfter(key reconcile.Request, after time.Duration) {
	q.AddWithOpts(priorityqueue.AddOpts{After: after}, key)
}
func (q *priorityGate) AddRateLimited(key reconcile.Request) {
	q.AddWithOpts(priorityqueue.AddOpts{RateLimited: true}, key)
}

func (q *priorityGate) AddWithOpts(o priorityqueue.AddOpts, keys ...reconcile.Request) {
	if o.After <= 0 && !o.RateLimited {
		q.mu.Lock()
		for _, key := range keys {
			q.items[key] = -1
		}
		q.mu.Unlock()
		q.PriorityQueue.AddWithOpts(o, keys...)
		return
	}
	ticks := int64(-1)
	if o.After > 0 {
		ticks = int64(o.After / time.Second)
		if o.After%time.Second != 0 {
			ticks++
		}
	}
	if o.RateLimited && (ticks < 0 || q.retryTicks < ticks) {
		ticks = q.retryTicks
	}
	due := q.owner.group.Now() + ticks
	q.mu.Lock()
	for _, key := range keys {
		if old, ok := q.items[key]; !ok || (old >= 0 && due < old) {
			q.items[key] = due
		}
	}
	q.mu.Unlock()
	native := priorityqueue.AddOpts{Priority: o.Priority}
	if o.RateLimited {
		// The rate limiter counts the retry and returns the parked delay.
		native.RateLimited = true
	} else {
		native.After = parked
	}
	q.PriorityQueue.AddWithOpts(native, keys...)
}

func (q *priorityGate) GetWithPriority() (reconcile.Request, int, bool) {
	select {
	case <-q.owner.group.ctx.Done():
		return reconcile.Request{}, 0, true
	case <-q.owner.permits:
	}
	key, priority, shutdown := q.PriorityQueue.GetWithPriority()
	if !shutdown {
		q.mu.Lock()
		delete(q.items, key)
		q.priorities[key] = priority
		q.mu.Unlock()
	}
	return key, priority, shutdown
}
func (q *priorityGate) Get() (reconcile.Request, bool) {
	key, _, shutdown := q.GetWithPriority()
	return key, shutdown
}
func (q *priorityGate) Done(key reconcile.Request) {
	q.PriorityQueue.Done(key)
	q.owner.finished(key)
}

// NumRequeues flushes the native add buffer first, because the native queue
// counts a rate-limited add only when it takes the add from its buffer.
func (q *priorityGate) NumRequeues(key reconcile.Request) int {
	q.PriorityQueue.Len()
	return q.PriorityQueue.NumRequeues(key)
}

func (q *priorityGate) taken(key reconcile.Request) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.priorities[key]
}
func (q *priorityGate) queued(key reconcile.Request) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	due, ok := q.items[key]
	return ok && due < 0
}
func (q *priorityGate) due(key reconcile.Request) int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	if due, ok := q.items[key]; ok {
		return due
	}
	return -1
}

// startable reports whether a ready item exists whose key is not in progress,
// so a Get will not block.
func (q *priorityGate) startable() bool {
	q.owner.mu.Lock()
	defer q.owner.mu.Unlock()
	q.mu.Lock()
	defer q.mu.Unlock()
	for key, due := range q.items {
		if due < 0 && q.owner.active[key] == nil {
			return true
		}
	}
	return false
}

func (q *priorityGate) readyCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, due := range q.items {
		if due < 0 {
			n++
		}
	}
	return n
}

// fire makes the waiting item of key ready through the native timer path and
// waits until the native queue reports it ready.
func (q *priorityGate) fire(key reconcile.Request) error {
	q.mu.Lock()
	due, ok := q.items[key]
	if !ok || due < 0 || due > q.owner.group.Now() {
		q.mu.Unlock()
		return fmt.Errorf("timer not due for %v", key)
	}
	q.items[key] = -1
	q.mu.Unlock()
	// The lowest priority never raises the item, so only the ready time moves.
	lowest := math.MinInt
	q.PriorityQueue.AddWithOpts(priorityqueue.AddOpts{After: time.Nanosecond, Priority: &lowest}, key)
	want := q.readyCount()
	ctx := q.owner.group.ctx
	for q.PriorityQueue.Len() != want {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Microsecond):
		}
	}
	return nil
}

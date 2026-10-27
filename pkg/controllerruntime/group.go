package controllerruntime

import (
	"context"
	"fmt"
	"generative-architecture/pkg/effect"
	"github.com/go-logr/logr"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sync"
	"sync/atomic"
	"time"
)

// Group coordinates real controllers with independent native queues and a
// shared logical clock. Each key has its own timer, retry count and invocation.
// Scheduling methods are called by one driver; reconcilers run concurrently.
type Group struct {
	ctx         context.Context
	cancel      context.CancelFunc
	now         atomic.Int64
	controllers map[string]*Controller
}
type ControllerOptions struct {
	Name string
	// Queue selects the native queue: QueueFIFO (the default) runs client-go's
	// rate-limited FIFO queue, QueuePriority controller-runtime's priority queue.
	Queue      string
	Workers    int
	Keys       []reconcile.Request
	RetryTicks int
	Reconciler reconcile.Reconciler
}
type Controller struct {
	group   *Group
	name    string
	workers int
	keys    map[reconcile.Request]bool
	queue   workqueue.TypedRateLimitingInterface[reconcile.Request]
	permits chan struct{}
	delay   *keyDelays    // FIFO timers
	pq      *priorityGate // priority queue, nil for FIFO
	entered chan *Invocation
	stopped chan error
	mu      sync.Mutex
	active  map[reconcile.Request]*Invocation
	serial  uint64
}
type Invocation struct {
	ID         string
	Controller string
	Key        reconcile.Request
	// Priority is the priority the native priority queue handed the key out
	// with. It is zero for a FIFO queue.
	Priority int
	Gate     *effect.Gate
	ctx      context.Context
	result   chan workerResult
	release  chan struct{}
	done     chan struct{}
	finished bool
}
type workerResult struct {
	Result reconcile.Result
	Err    error
}
type invocationKey struct{}

func CurrentInvocation(ctx context.Context) *Invocation {
	v, _ := ctx.Value(invocationKey{}).(*Invocation)
	return v
}

// InvocationHook routes boundaries to the current invocation's own gate.
func InvocationHook(ctx context.Context, c effect.Call, next func() effect.Result) effect.Result {
	inv := CurrentInvocation(ctx)
	if inv == nil {
		return effect.Result{Err: fmt.Errorf("effect outside controlled invocation")}
	}
	c.Invocation = inv.ID
	return inv.Gate.Hook(ctx, c, next)
}
func NewGroup(ctx context.Context) *Group {
	ctx, cancel := context.WithCancel(ctx)
	return &Group{ctx: ctx, cancel: cancel, controllers: map[string]*Controller{}}
}
func (g *Group) Add(o ControllerOptions) (*Controller, error) {
	if o.Name == "" || g.controllers[o.Name] != nil || o.Workers < 1 || o.RetryTicks < 1 || len(o.Keys) == 0 || o.Reconciler == nil {
		return nil, fmt.Errorf("invalid/duplicate controller profile")
	}
	c := &Controller{group: g, name: o.Name, workers: o.Workers, keys: map[reconcile.Request]bool{}, active: map[reconcile.Request]*Invocation{}, entered: make(chan *Invocation, o.Workers), stopped: make(chan error, 1)}
	for _, key := range o.Keys {
		if key.Name == "" || c.keys[key] {
			return nil, fmt.Errorf("invalid/duplicate key")
		}
		c.keys[key] = true
	}
	c.permits = make(chan struct{})
	usePriority := false
	switch o.Queue {
	case "", QueueFIFO:
		d := &keyDelays{TypedInterface: workqueue.NewTyped[reconcile.Request](), group: g, due: map[reconcile.Request]int64{}}
		c.delay = d
		native := workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](time.Duration(o.RetryTicks)*time.Second, time.Duration(o.RetryTicks)*time.Second), workqueue.TypedRateLimitingQueueConfig[reconcile.Request]{DelayingQueue: d})
		c.queue = &multiGate{TypedRateLimitingInterface: native, owner: c}
	case QueuePriority:
		usePriority = true
		c.pq = newPriorityGate(c, o.RetryTicks)
		c.queue = c.pq
	default:
		return nil, fmt.Errorf("unknown queue %q", o.Queue)
	}
	yes := true
	ready := make(chan struct{})
	ctrl, err := controller.NewUnmanaged(o.Name, controller.Options{SkipNameValidation: &yes, UsePriorityQueue: &usePriority, MaxConcurrentReconciles: o.Workers, Logger: logr.Discard(),
		NewQueue: func(string, workqueue.TypedRateLimiter[reconcile.Request]) workqueue.TypedRateLimitingInterface[reconcile.Request] {
			close(ready)
			return c.queue
		},
		Reconciler: reconcile.Func(func(ctx context.Context, key reconcile.Request) (reconcile.Result, error) {
			c.mu.Lock()
			c.serial++
			inv := &Invocation{ID: fmt.Sprintf("%s/%d", c.name, c.serial), Controller: c.name, Key: key, Gate: effect.NewGate(), ctx: ctx, result: make(chan workerResult, 1), release: make(chan struct{}), done: make(chan struct{})}
			if c.pq != nil {
				inv.Priority = c.pq.taken(key)
			}
			c.active[key] = inv
			c.mu.Unlock()
			ctx = context.WithValue(ctx, invocationKey{}, inv)
			select {
			case c.entered <- inv:
			case <-ctx.Done():
				return reconcile.Result{}, ctx.Err()
			}
			result, err := o.Reconciler.Reconcile(ctx, key)
			inv.result <- workerResult{result, err}
			select {
			case <-inv.release:
				return result, err
			case <-ctx.Done():
				return reconcile.Result{}, ctx.Err()
			}
		}),
	})
	if err != nil {
		c.queue.ShutDown()
		return nil, err
	}
	g.controllers[o.Name] = c
	go func() { c.stopped <- ctrl.Start(g.ctx) }()
	select {
	case <-ready:
		return c, nil
	case <-g.ctx.Done():
		return nil, g.ctx.Err()
	}
}

// multiGate holds a FIFO queue's Get until the driver grants a permit.
type multiGate struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	owner *Controller
}

func (q *multiGate) Get() (reconcile.Request, bool) {
	select {
	case <-q.owner.group.ctx.Done():
		return reconcile.Request{}, true
	case <-q.owner.permits:
		return q.TypedRateLimitingInterface.Get()
	}
}
func (q *multiGate) Done(key reconcile.Request) {
	q.TypedRateLimitingInterface.Done(key)
	q.owner.finished(key)
}

// finished releases the driver waiting for the invocation of key after the
// native queue's Done.
func (c *Controller) finished(key reconcile.Request) {
	c.mu.Lock()
	inv := c.active[key]
	delete(c.active, key)
	c.mu.Unlock()
	if inv != nil {
		close(inv.done)
	}
}

type keyDelays struct {
	workqueue.TypedInterface[reconcile.Request]
	mu    sync.Mutex
	group *Group
	due   map[reconcile.Request]int64
}

func (d *keyDelays) AddAfter(key reconcile.Request, delay time.Duration) {
	if delay <= 0 {
		d.Add(key)
		return
	}
	ticks := int64(delay / time.Second)
	if delay%time.Second != 0 {
		ticks++
	}
	due := d.group.now.Load() + ticks
	d.mu.Lock()
	defer d.mu.Unlock()
	if old, ok := d.due[key]; !ok || due < old {
		d.due[key] = due
	}
}
func (c *Controller) Enqueue(key reconcile.Request) error {
	if !c.keys[key] {
		return fmt.Errorf("key outside declared domain: %v", key)
	}
	c.queue.Add(key)
	return nil
}

// EnqueueLow adds key as an initial-list event does. A priority queue gets
// it at LowPriority; a FIFO queue gets a plain Add, like controller-runtime's
// handlers do for a queue that is not a priority queue.
func (c *Controller) EnqueueLow(key reconcile.Request) error {
	if !c.keys[key] {
		return fmt.Errorf("key outside declared domain: %v", key)
	}
	if c.pq == nil {
		c.queue.Add(key)
		return nil
	}
	low := LowPriority
	c.pq.AddWithOpts(priorityqueue.AddOpts{Priority: &low}, key)
	return nil
}

// Ready is the native queue's length. A priority queue also counts ready
// items whose key is still in progress.
func (c *Controller) Ready() int                          { return c.queue.Len() }
func (c *Controller) Retries(key reconcile.Request) int64 { return int64(c.queue.NumRequeues(key)) }

// Queued reports whether key has a ready item in a priority queue. It is
// false for a FIFO queue.
func (c *Controller) Queued(key reconcile.Request) bool {
	return c.pq != nil && c.pq.queued(key)
}
func (c *Controller) Due(key reconcile.Request) int64 {
	if c.pq != nil {
		return c.pq.due(key)
	}
	c.delay.mu.Lock()
	defer c.delay.mu.Unlock()
	v, ok := c.delay.due[key]
	if !ok {
		return -1
	}
	return v
}
func (c *Controller) Active() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.active) }

// Start takes the key the native queue hands out next; it never reorders the
// queue to fit a trace.
func (c *Controller) Start() (*Invocation, error) {
	startable := c.Ready() > 0
	if c.pq != nil {
		startable = c.pq.startable()
	}
	if !startable || c.Active() >= c.workers {
		return nil, fmt.Errorf("no ready key or all workers busy")
	}
	select {
	case c.permits <- struct{}{}:
	case <-c.group.ctx.Done():
		return nil, c.group.ctx.Err()
	}
	select {
	case inv := <-c.entered:
		return inv, nil
	case <-c.group.ctx.Done():
		return nil, c.group.ctx.Err()
	}
}
func (inv *Invocation) Finish() (reconcile.Result, error, error) {
	if inv.finished {
		return reconcile.Result{}, nil, fmt.Errorf("invocation already finished")
	}
	inv.finished = true
	var result workerResult
	select {
	case result = <-inv.result:
	case <-inv.ctx.Done():
		return reconcile.Result{}, nil, inv.ctx.Err()
	}
	select {
	case inv.release <- struct{}{}:
	case <-inv.ctx.Done():
		return reconcile.Result{}, nil, inv.ctx.Err()
	}
	select {
	case <-inv.done:
		return result.Result, result.Err, nil
	case <-inv.ctx.Done():
		return reconcile.Result{}, nil, inv.ctx.Err()
	}
}
func (g *Group) Now() int64 { return g.now.Load() }
func (g *Group) Advance()   { g.now.Add(1) }
func (c *Controller) Fire(key reconcile.Request) error {
	if c.pq != nil {
		return c.pq.fire(key)
	}
	c.delay.mu.Lock()
	defer c.delay.mu.Unlock()
	due, ok := c.delay.due[key]
	if !ok || due > c.group.Now() {
		return fmt.Errorf("timer not due for %v", key)
	}
	delete(c.delay.due, key)
	c.delay.Add(key)
	return nil
}
func (g *Group) Close() error {
	g.cancel()
	var first error
	for _, c := range g.controllers {
		select {
		case err := <-c.stopped:
			if first == nil {
				first = err
			}
		case <-time.After(time.Second):
			if first == nil {
				first = fmt.Errorf("controller %s failed to stop", c.name)
			}
		}
	}
	return first
}

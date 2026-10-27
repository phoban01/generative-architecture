// Runtime profile conformance uses a scripted reconciler whose real result is
// handled by controller-runtime. It tests the runtime, not business behaviour.
package controllerruntime

import (
	"context"
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/model"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

type runtimeAdapter struct {
	m       GroupModel
	g       *Group
	c       map[string]*Controller
	inv     map[string]*Invocation
	replies map[string]chan string
	dirty   map[string]bool
	order   map[string]int64
	next    map[string]int64
	// Priority queue bookkeeping: the queue kind per controller, and per key
	// the item's priority and the priority its invocation was taken with.
	queue    map[string]string
	priority map[string]int64
	taken    map[string]int64
	deleted  map[string]bool
}

func request(k ModelKey) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKey{Namespace: k.Namespace, Name: k.Name}}
}
func newRuntimeAdapter(ctx context.Context, m GroupModel) (*runtimeAdapter, error) {
	a := &runtimeAdapter{m: m, g: NewGroup(ctx), c: map[string]*Controller{}, inv: map[string]*Invocation{}, replies: map[string]chan string{}, dirty: map[string]bool{}, order: map[string]int64{}, next: map[string]int64{}, queue: map[string]string{}, priority: map[string]int64{}, taken: map[string]int64{}, deleted: map[string]bool{}}
	for _, cfg := range m.Profile.Controllers {
		queue, err := m.Profile.QueueOf(cfg)
		if err != nil {
			a.g.Close()
			return nil, err
		}
		a.queue[cfg.Name] = queue
		keys := []reconcile.Request{}
		lookup := map[reconcile.Request]string{}
		for _, k := range cfg.Keys {
			keys = append(keys, request(k))
			lookup[request(k)] = k.ID
			a.replies[cfg.Name+"_"+k.ID] = make(chan string)
		}
		c, err := a.g.Add(ControllerOptions{Name: cfg.Name, Queue: queue, Startup: cfg.Startup, Workers: cfg.Workers, Keys: keys, RetryTicks: cfg.RetryTicks, Reconciler: reconcile.Func(func(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
			var outcome string
			select {
			case outcome = <-a.replies[cfg.Name+"_"+lookup[req]]:
			case <-ctx.Done():
				return reconcile.Result{}, nil
			}
			switch outcome {
			case "error":
				return reconcile.Result{}, fmt.Errorf("transient")
			case "requeue":
				return reconcile.Result{Requeue: true}, nil
			case "after":
				return reconcile.Result{RequeueAfter: time.Duration(cfg.AfterTicks) * time.Second}, nil
			case "terminal":
				return reconcile.Result{}, reconcile.TerminalError(fmt.Errorf("terminal"))
			case "errorAfter":
				return reconcile.Result{RequeueAfter: time.Duration(cfg.AfterTicks) * time.Second}, fmt.Errorf("transient")
			}
			return reconcile.Result{}, nil
		})})
		if err != nil {
			a.g.Close()
			return nil, err
		}
		a.c[cfg.Name] = c
	}
	return a, nil
}
func (a *runtimeAdapter) key(controller, id string) ModelKey {
	for _, c := range a.m.Profile.Controllers {
		if c.Name == controller {
			for _, k := range c.Keys {
				if k.ID == id {
					return k
				}
			}
		}
	}
	panic("unknown test event")
}
func (a *runtimeAdapter) queued(prefix, controller string) {
	if !a.dirty[prefix] && a.inv[prefix] == nil {
		a.order[prefix] = a.next[controller]
		a.next[controller]++
	}
	a.dirty[prefix] = true
}
func (a *runtimeAdapter) Apply(ctx context.Context, name string) error {
	e, ok := a.m.Events[name]
	if !ok {
		return fmt.Errorf("unknown event")
	}
	if e.Kind == "advance" {
		a.g.Advance()
		return nil
	}
	c := a.c[e.Controller]
	if e.Kind == "sync" {
		return c.Sync()
	}
	prefix := e.Controller + "_" + e.Key
	key := request(a.key(e.Controller, e.Key))
	if e.Kind == "tombstone" {
		a.deleted[prefix] = true
	}
	if a.queue[e.Controller] == QueuePriority {
		return a.applyPriority(ctx, e, prefix, c, key)
	}
	switch e.Kind {
	case "enqueue", "tombstone":
		if err := c.Enqueue(key); err != nil {
			return err
		}
		a.queued(prefix, e.Controller)
	case "list":
		if err := c.EnqueueLow(key); err != nil {
			return err
		}
		a.queued(prefix, e.Controller)
	case "fire":
		if err := c.Fire(key); err != nil {
			return err
		}
		a.queued(prefix, e.Controller)
	case "start":
		inv, err := c.Start()
		if err != nil {
			return err
		}
		if inv.Key != key {
			return fmt.Errorf("FIFO mismatch: expected %v got %v", key, inv.Key)
		}
		a.inv[prefix] = inv
		a.dirty[prefix] = false
	case "finish":
		select {
		case a.replies[prefix] <- e.Outcome:
		case <-ctx.Done():
			return ctx.Err()
		}
		_, _, err := a.inv[prefix].Finish()
		if err != nil {
			return err
		}
		delete(a.inv, prefix)
		if a.dirty[prefix] {
			a.order[prefix] = a.next[e.Controller]
			a.next[e.Controller]++
		}
	}
	return nil
}
func (a *runtimeAdapter) Project(context.Context) (map[string]any, error) {
	out := map[string]any{"now": a.g.Now()}
	for _, c := range a.m.Profile.Controllers {
		out[c.Name+"_next"] = a.next[c.Name]
		if c.Startup {
			out[c.Name+"_synced"] = a.c[c.Name].Synced()
		}
		if c.Deletion {
			for _, k := range c.Keys {
				out[c.Name+"_"+k.ID+"_deleted"] = a.deleted[c.Name+"_"+k.ID]
			}
		}
		if a.queue[c.Name] == QueuePriority {
			if err := a.projectPriority(c, out); err != nil {
				return nil, err
			}
			continue
		}
		ready := 0
		for _, k := range c.Keys {
			prefix := c.Name + "_" + k.ID
			processing := a.inv[prefix] != nil
			isReady := a.dirty[prefix] && !processing
			if isReady {
				ready++
			}
			out[prefix+"_ready"] = isReady
			out[prefix+"_dirty"] = a.dirty[prefix]
			out[prefix+"_processing"] = processing
			out[prefix+"_due"] = a.c[c.Name].Due(request(k))
			out[prefix+"_retries"] = a.c[c.Name].Retries(request(k))
			out[prefix+"_order"] = a.order[prefix]
		}
		if a.c[c.Name].Ready() != ready {
			return nil, fmt.Errorf("native ready count differs")
		}
	}
	return out, nil
}

func ReplayGroup(ctx context.Context, m GroupModel, trace conformance.Trace) (conformance.Report, error) {
	compiled, err := model.Compile(m.Spec)
	if err != nil {
		return conformance.Report{}, err
	}
	adapter, err := newRuntimeAdapter(ctx, m)
	if err != nil {
		return conformance.Report{}, err
	}
	report, err := conformance.Replay(ctx, compiled, trace, adapter)
	closed := adapter.g.Close()
	if err == nil {
		err = closed
	}
	return report, err
}

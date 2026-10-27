package controllerruntime

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// applyPriority drives a controller whose native queue is the priority
// queue. The native queue chooses the key at each start; the adapter only
// keeps the priority and counter of each item, which the queue does not
// expose, and checks the priority the queue hands out.
func (a *runtimeAdapter) applyPriority(ctx context.Context, e RuntimeEvent, prefix string, c *Controller, key reconcile.Request) error {
	present := c.Queued(key) || c.Due(key) >= 0
	switch e.Kind {
	case "enqueue":
		if err := c.Enqueue(key); err != nil {
			return err
		}
		a.merge(prefix, e.Controller, present, 0)
	case "enqueueLow":
		if err := c.EnqueueLow(key); err != nil {
			return err
		}
		a.merge(prefix, e.Controller, present, LowPriority)
	case "fire":
		if err := c.Fire(key); err != nil {
			return err
		}
		a.order[prefix] = a.next[e.Controller]
		a.next[e.Controller]++
	case "start":
		inv, err := c.Start()
		if err != nil {
			return err
		}
		if inv.Key != key {
			return fmt.Errorf("priority queue mismatch: expected %v got %v", key, inv.Key)
		}
		if int64(inv.Priority) != a.priority[prefix] {
			return fmt.Errorf("priority queue handed out %v at priority %d, expected %d", key, inv.Priority, a.priority[prefix])
		}
		a.inv[prefix] = inv
		a.taken[prefix] = a.priority[prefix]
	case "finish":
		select {
		case a.replies[prefix] <- e.Outcome:
		case <-ctx.Done():
			return ctx.Err()
		}
		if _, _, err := a.inv[prefix].Finish(); err != nil {
			return err
		}
		delete(a.inv, prefix)
		switch e.Outcome {
		case "error", "requeue", "errorAfter", "after":
			a.merge(prefix, e.Controller, present, int(a.taken[prefix]))
		}
	default:
		return fmt.Errorf("unknown priority queue event %s", e.Kind)
	}
	return nil
}

// merge records an add at priority p: the item keeps the higher priority and
// takes a new counter when it is new or its priority rises.
func (a *runtimeAdapter) merge(prefix, controller string, present bool, p int) {
	priority := int64(p)
	if !present || priority > a.priority[prefix] {
		a.order[prefix] = a.next[controller]
		a.next[controller]++
		a.priority[prefix] = priority
	}
}

func (a *runtimeAdapter) projectPriority(c ModelController, out map[string]any) error {
	native := a.c[c.Name]
	queued := 0
	for _, k := range c.Keys {
		prefix := c.Name + "_" + k.ID
		isQueued := native.Queued(request(k))
		if isQueued {
			queued++
		}
		out[prefix+"_queued"] = isQueued
		out[prefix+"_processing"] = a.inv[prefix] != nil
		out[prefix+"_due"] = native.Due(request(k))
		out[prefix+"_retries"] = native.Retries(request(k))
		out[prefix+"_order"] = a.order[prefix]
		out[prefix+"_priority"] = a.priority[prefix]
		out[prefix+"_taken"] = a.taken[prefix]
	}
	if native.Ready() != queued {
		return fmt.Errorf("native ready count %d differs from %d queued keys", native.Ready(), queued)
	}
	return nil
}

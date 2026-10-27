package controllerruntime

import (
	"fmt"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/model"
	"strconv"
	"strings"
)

// LowPriority is the priority controller-runtime gives to events from the
// initial list (handler.LowPriority in sigs.k8s.io/controller-runtime v0.23.3).
const LowPriority = -100

// addPriorityController adds one controller whose work queue is
// controller-runtime's priority queue. The summary follows
// sigs.k8s.io/controller-runtime v0.23.3 pkg/controller/priorityqueue/priorityqueue.go
// and the reconcileHandler in pkg/internal/controller/controller.go:
//
//   - The queue holds at most one item per key. An item is ready or waiting
//     for a timer. A key in progress can also have an item: an add during
//     processing does not wait for Done, but Get skips the key until Done.
//   - A new item takes the next added counter. Get hands out the ready,
//     unlocked item with the highest priority, then the lowest counter.
//   - An add to an existing item keeps the higher priority. A strictly
//     higher priority also takes a new counter. An add without a delay makes
//     a waiting item ready and keeps its counter. A delayed add keeps the
//     earlier ready time and leaves a ready item ready.
//   - A waiting item that becomes ready by time takes a new counter.
//   - The handler re-adds a failed or requeued key with the priority it was
//     taken with: rate limited after an error or Requeue, after the delay for
//     RequeueAfter (which also forgets the retries).
//   - Events from the initial list are added at LowPriority.
//
// Per key, queued means a ready item, due >= 0 a waiting item with that
// ready time, priority and order the item's priority and counter, and taken
// the priority of the running invocation. next is the added counter.
func addPriorityController(add func(string, RuntimeEvent, string, map[string]expr.Expression), spec *model.Spec, c ModelController) error {
	next := c.Name + "_next"
	spec.State[next] = model.Variable{Type: "int", Initial: "0"}
	keys := map[string]bool{}
	objects := map[string]bool{}
	active := []string{}
	for _, k := range c.Keys {
		if k.ID == "" || k.Name == "" || keys[k.ID] || objects[k.Namespace+"/"+k.Name] {
			return fmt.Errorf("invalid/duplicate model key")
		}
		keys[k.ID] = true
		objects[k.Namespace+"/"+k.Name] = true
		prefix := c.Name + "_" + k.ID
		for _, v := range []string{"queued", "processing"} {
			spec.State[prefix+"_"+v] = model.Variable{Type: "bool", Initial: "false"}
		}
		for _, v := range []string{"retries", "order", "priority", "taken"} {
			spec.State[prefix+"_"+v] = model.Variable{Type: "int", Initial: "0"}
		}
		spec.State[prefix+"_due"] = model.Variable{Type: "int", Initial: "-1"}
		active = append(active, "("+prefix+"_processing ? 1 : 0)")
	}
	for _, k := range c.Keys {
		prefix := c.Name + "_" + k.ID
		queued, processing, priority, taken, order, due, retries := prefix+"_queued", prefix+"_processing", prefix+"_priority", prefix+"_taken", prefix+"_order", prefix+"_due", prefix+"_retries"
		present := fmt.Sprintf("(%s || %s >= 0)", queued, due)
		// merge returns the updates of an add with priority p (an expression)
		// to the item's priority and counter.
		merge := func(p string) map[string]expr.Expression {
			bump := fmt.Sprintf("!%s || %s > %s", present, p, priority)
			return map[string]expr.Expression{
				priority: expr.Expression(fmt.Sprintf("%s && %s > %s ? %s : %s", present, priority, p, priority, p)),
				order:    expr.Expression(fmt.Sprintf("%s ? %s : %s", bump, next, order)),
				next:     expr.Expression(fmt.Sprintf("%s ? %s + 1 : %s", bump, next, next)),
			}
		}
		// enqueue is an add without delay.
		enqueue := func(p string) map[string]expr.Expression {
			u := merge(p)
			u[queued] = "true"
			u[due] = "-1"
			return u
		}
		add(prefix+"_enqueue", RuntimeEvent{c.Name, k.ID, "enqueue", ""}, "true", enqueue("0"))
		add(prefix+"_enqueueLow", RuntimeEvent{c.Name, k.ID, "enqueueLow", ""}, "true", enqueue(strconv.Itoa(LowPriority)))
		guard := fmt.Sprintf("%s && !%s && (%s) < %d", queued, processing, strings.Join(active, " + "), c.Workers)
		for _, other := range c.Keys {
			if other.ID != k.ID {
				op := c.Name + "_" + other.ID
				guard += fmt.Sprintf(" && (!(%s_queued && !%s_processing) || %s > %s_priority || (%s == %s_priority && %s < %s_order))", op, op, priority, op, priority, op, order, op)
			}
		}
		add(prefix+"_start", RuntimeEvent{c.Name, k.ID, "start", ""}, guard, map[string]expr.Expression{queued: "false", processing: "true", taken: expr.Expression(priority)})
		add(prefix+"_fire", RuntimeEvent{c.Name, k.ID, "fire", ""}, due+" >= 0 && now >= "+due, map[string]expr.Expression{due: "-1", queued: "true", order: expr.Expression(next), next: expr.Expression(next + " + 1")})
		for _, outcome := range []string{"success", "error", "requeue", "after", "terminal", "errorAfter"} {
			u := map[string]expr.Expression{processing: "false"}
			delay := 0
			switch outcome {
			case "success":
				u[retries] = "0"
			case "error", "requeue", "errorAfter":
				u[retries] = expr.Expression(retries + " + 1")
				delay = c.RetryTicks
			case "after":
				u[retries] = "0"
				delay = c.AfterTicks
			}
			if delay > 0 {
				for name, v := range merge(taken) {
					u[name] = v
				}
				u[due] = expr.Expression(fmt.Sprintf("%s ? -1 : (%s < 0 || now + %d < %s ? now + %d : %s)", queued, due, delay, due, delay, due))
			}
			add(prefix+"_"+outcome, RuntimeEvent{c.Name, k.ID, "finish", outcome}, processing, u)
		}
		spec.Properties = append(spec.Properties, model.Property{Name: prefix + "_single", Expression: expr.Expression("!(" + queued + " && " + due + " >= 0)")})
	}
	spec.Properties = append(spec.Properties, model.Property{Name: c.Name + "_capacity", Expression: expr.Expression("(" + strings.Join(active, " + ") + ") <= " + strconv.Itoa(c.Workers))})
	return nil
}

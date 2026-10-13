package controllerruntime

import "fmt"

// QueueProfile specifies logical delays. The profile models one key/worker and
// client-go FIFO semantics, not controller-runtime's default priority queue.
type QueueProfile struct{ RetryDelayTicks, RequeueAfterTicks int }

// QueueHelpers emits reusable Quint record transformations. Callers supply a
// record with ready, dirty, processing, due, now and retries fields.
func QueueHelpers(p QueueProfile) string {
	return fmt.Sprintf(`
 pure def earliest(old, updated) = if (old < 0 or updated < old) updated else old
 pure def queueEnqueue(q) = q.with("dirty",true).with("ready",q.ready or not(q.processing))
 pure def queueStart(q) = q.with("ready",false).with("dirty",false).with("processing",true)
 pure def queueFire(q) = queueEnqueue(q.with("due",-1))
 pure def queueComplete(q,outcome) = {
  val failed = outcome == "error" or outcome == "requeue" or outcome == "error-with-after"
  val after = outcome == "after"
  val nextDue = if (failed) earliest(q.due,q.now+%d) else if (after) earliest(q.due,q.now+%d) else q.due
  val nextRetries = if (failed) q.retries+1 else if (outcome == "terminal") q.retries else 0
  q.with("processing",false).with("ready",q.dirty).with("due",nextDue).with("retries",nextRetries)
 }
`, p.RetryDelayTicks, p.RequeueAfterTicks)
}

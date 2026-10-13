// Runtime library model: reviewed semantics, not extracted from controller-runtime.
// One reconcile key, one worker. Logical timers, not wall-clock performance.
// publish is a macro-step calling real MAC.Publish in replay; its internal race
// remains covered separately by the source-extracted Publication model.
module Runtime {
 var s: { ready: bool, processing: bool, dirty: bool, due: int, now: int, retries: int,
          lease: bool, cached: bool, observed: bool, published: bool, accepted: bool, fresh: bool }
 var event: str
 var tick: int
 action init = all {
  s' = { ready: false, processing: false, dirty: false, due: -1, now: 0, retries: 0,
         lease: true, cached: true, observed: false, published: false, accepted: false, fresh: true },
  event' = "init", tick' = 0
 }
 
 pure def earliest(old, updated) = if (old < 0 or updated < old) updated else old
 pure def queueEnqueue(q) = q.with("dirty",true).with("ready",q.ready or not(q.processing))
 pure def queueStart(q) = q.with("ready",false).with("dirty",false).with("processing",true)
 pure def queueFire(q) = queueEnqueue(q.with("due",-1))
 pure def queueComplete(q,outcome) = {
  val failed = outcome == "error" or outcome == "requeue" or outcome == "error-with-after"
  val after = outcome == "after"
  val nextDue = if (failed) earliest(q.due,q.now+2) else if (after) earliest(q.due,q.now+3) else q.due
  val nextRetries = if (failed) q.retries+1 else if (outcome == "terminal") q.retries else 0
  q.with("processing",false).with("ready",q.dirty).with("due",nextDue).with("retries",nextRetries)
 }

 action change(label, updated) = all {s' = updated, event' = label, tick' = tick + 1}
 action enqueue = change("enqueue", queueEnqueue(s))
 action start = all {s.ready, not(s.processing), change("start",queueStart(s))}
 action finish(outcome) = all {s.processing, change(outcome,queueComplete(s,outcome))}
 action timePasses = all { s.now < 20, change("time",s.with("now",s.now+1)) }
 action fire = all {s.due >= 0, s.now >= s.due,
  change("fire",queueFire(s)) }
 action revoke = change("revoke",s.with("lease",false))
 action syncCache = change("cache-sync",s.with("cached",s.lease))
 action syncInfraCache = change("infra-cache-sync",s.with("fresh",true))
 action observe = all {s.processing,change("observe",s.with("observed",s.cached))}
 action publish = all {s.processing,change("publish",s.with("accepted",s.observed and s.lease and s.fresh).with("published",s.published or (s.observed and s.lease and s.fresh)).with("fresh",s.fresh and not(s.observed and s.lease)))}
 action idle = change("idle",s)
 action step = any {enqueue,start,finish("success"),finish("error"),finish("requeue"),finish("after"),finish("terminal"),finish("error-with-after"),timePasses,fire,revoke,syncCache,syncInfraCache,observe,publish,idle}
 val queueSafety = (not(s.ready and s.processing)) and (not(s.ready) or s.dirty)
 val publicationChecksDirectRead = event != "publish" or not(s.accepted) or s.lease
 val runtimeSafety = queueSafety and publicationChecksDirectRead
 // Intentionally false property: cache delivery can lag API writes.
 val cacheIsAlwaysFresh = s.cached == s.lease
 action publicationStep = if (tick == 0) enqueue else if (tick == 1) start else if (tick == 2) observe else if (tick == 3) publish else if (tick == 4) publish else if (tick == 5) syncInfraCache else if (tick == 6) publish else if (tick == 7) finish("success") else idle
 action staleCacheStep = if (tick == 0) enqueue else if (tick == 1) start else if (tick == 2) revoke else if (tick == 3) observe else if (tick == 4) publish else if (tick == 5) finish("success") else idle
 action dirtyStep = if (tick == 0) enqueue else if (tick == 1) enqueue else if (tick == 2) start else if (tick == 3) enqueue else if (tick == 4) enqueue else if (tick == 5) finish("success") else if (tick == 6) start else if (tick == 7) finish("success") else idle
 action retryStep = if (tick == 0) enqueue else if (tick == 1) start else if (tick == 2) finish("error-with-after") else if (tick == 3) timePasses else if (tick == 4) timePasses else if (tick == 5) fire else if (tick == 6) start else if (tick == 7) finish("after") else if (tick == 8) timePasses else if (tick == 9) timePasses else if (tick == 10) timePasses else if (tick == 11) fire else if (tick == 12) start else if (tick == 13) finish("terminal") else idle
 action timerSurvivesSuccessStep = if (tick == 0) enqueue else if (tick == 1) start else if (tick == 2) finish("after") else if (tick == 3) enqueue else if (tick == 4) start else if (tick == 5) finish("success") else if (tick == 6) timePasses else if (tick == 7) timePasses else if (tick == 8) timePasses else if (tick == 9) fire else if (tick == 10) start else if (tick == 11) finish("success") else idle
}

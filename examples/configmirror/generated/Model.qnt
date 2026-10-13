// Generated from a typed TransitionModel; declared semantics, not inferred intent.
module ConfigMirror {
 var cached: str
 var dirty: bool
 var due: int
 var failed: bool
 var now: int
 var pc: int
 var processing: bool
 var ready: bool
 var retries: int
 var snapshot: str
 var source: str
 var target: str
 var event: str
 action init = all { event' = "init", cached' = "a", dirty' = false, due' = -1, failed' = false, now' = 0, pc' = 0, processing' = false, ready' = false, retries' = 0, snapshot' = "", source' = "a", target' = "" }
 action changeSource = all { true, event' = "changeSource", cached' = cached, dirty' = dirty, due' = due, failed' = failed, now' = now, pc' = pc, processing' = processing, ready' = ready, retries' = retries, snapshot' = snapshot, source' = (if ((source == "a")) "b" else "a"), target' = target }
 action deliverCache = all { true, event' = "deliverCache", cached' = source, dirty' = dirty, due' = due, failed' = failed, now' = now, pc' = pc, processing' = processing, ready' = ready, retries' = retries, snapshot' = snapshot, source' = source, target' = target }
 action enqueue = all { true, event' = "enqueue", cached' = cached, dirty' = true, due' = due, failed' = failed, now' = now, pc' = pc, processing' = processing, ready' = (ready or not(processing)), retries' = retries, snapshot' = snapshot, source' = source, target' = target }
 action begin = all { ((ready and not(processing)) and (retries < 3)), event' = "begin", cached' = cached, dirty' = false, due' = due, failed' = false, now' = now, pc' = 1, processing' = true, ready' = false, retries' = retries, snapshot' = "", source' = source, target' = target }
 action readSource = all { (pc == 1), event' = "readSource", cached' = cached, dirty' = dirty, due' = due, failed' = failed, now' = now, pc' = 2, processing' = processing, ready' = ready, retries' = retries, snapshot' = cached, source' = source, target' = target }
 action readSourceError = all { (pc == 1), event' = "readSourceError", cached' = cached, dirty' = dirty, due' = due, failed' = true, now' = now, pc' = 4, processing' = processing, ready' = ready, retries' = retries, snapshot' = snapshot, source' = source, target' = target }
 action readTarget = all { (pc == 2), event' = "readTarget", cached' = cached, dirty' = dirty, due' = due, failed' = failed, now' = now, pc' = 3, processing' = processing, ready' = ready, retries' = retries, snapshot' = snapshot, source' = source, target' = target }
 action readTargetError = all { (pc == 2), event' = "readTargetError", cached' = cached, dirty' = dirty, due' = due, failed' = true, now' = now, pc' = 4, processing' = processing, ready' = ready, retries' = retries, snapshot' = snapshot, source' = source, target' = target }
 action commit = all { (pc == 3), event' = "commit", cached' = cached, dirty' = dirty, due' = due, failed' = failed, now' = now, pc' = 4, processing' = processing, ready' = ready, retries' = retries, snapshot' = snapshot, source' = source, target' = snapshot }
 action writeError = all { (pc == 3), event' = "writeError", cached' = cached, dirty' = dirty, due' = due, failed' = true, now' = now, pc' = 4, processing' = processing, ready' = ready, retries' = retries, snapshot' = snapshot, source' = source, target' = target }
 action finish = all { ((pc == 4) and processing), event' = "finish", cached' = cached, dirty' = dirty, due' = (if (failed) (if (((due < 0) or ((now + 2) < due))) (now + 2) else due) else due), failed' = failed, now' = now, pc' = 0, processing' = false, ready' = dirty, retries' = (if (failed) (retries + 1) else 0), snapshot' = snapshot, source' = source, target' = target }
 action advance = all { (now < 20), event' = "advance", cached' = cached, dirty' = dirty, due' = due, failed' = failed, now' = (now + 1), pc' = pc, processing' = processing, ready' = ready, retries' = retries, snapshot' = snapshot, source' = source, target' = target }
 action fire = all { ((due >= 0) and (now >= due)), event' = "fire", cached' = cached, dirty' = true, due' = -1, failed' = failed, now' = now, pc' = pc, processing' = processing, ready' = (ready or not(processing)), retries' = retries, snapshot' = snapshot, source' = source, target' = target }
 action step = any { changeSource, deliverCache, enqueue, begin, readSource, readSourceError, readTarget, readTargetError, commit, writeError, finish, advance, fire }
 val queueSafety = (not((ready and processing)) and (not(ready) or dirty))
 val writesObservedValue = ((event != "commit") or (target == snapshot))
 val boundedRetries = ((retries >= 0) and (retries <= 3))
 val safety = queueSafety and writesObservedValue and boundedRetries
}

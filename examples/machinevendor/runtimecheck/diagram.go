package runtimecheck

const runtimeDiagram = `// Generated from the supported RuntimeProfile; declared assumptions, not inferred wiring.
specification { element runtimePart }
model {
 runtimeAPI = runtimePart "API state" { description "Authoritative object state, represented by a fake API client in this harness." }
 runtimeCache = runtimePart "Informer cache" { description "Independent snapshots; delivery can lag writes. Real informer delivery is tested separately with a fake watch transport." }
 runtimeQueue = runtimePart "Workqueue" { description "Explicit client-go FIFO profile: dirty, processing and delayed work. This is not the v0.23.3 default priority queue." }
 runtimeWorker = runtimePart "Controller worker" { description "Real controller-runtime v0.23.3 loop; one key, one worker. Handles success, transient/terminal errors and requeues." }
 runtimeProcess = runtimePart "MAC processes" { description "Real Observe and Publish methods run inside a scripted reconciler. Parent Reconcile tested separately. This view shows the coarse baseline; the composed view replays the publication read/write interval." }
 runtimeAPI -> runtimeCache "watch delivery may lag"
 runtimeCache -> runtimeQueue "event to key (declared)"
 runtimeQueue -> runtimeWorker "Get / Done; key coalescing"
 runtimeWorker -> runtimeProcess "invoke process"
 runtimeProcess -> runtimeCache "Client reads"
 runtimeProcess -> runtimeAPI "APIReader and status writes"
 runtimeWorker -> runtimeQueue "result / error / requeue"
}
views { view runtime_conformance {
 title 'Runtime conformance: explicit FIFO demo profile'
 include runtimeAPI, runtimeCache, runtimeQueue, runtimeWorker, runtimeProcess
 autoLayout LeftRight
} }
`

# Controller-runtime conformance

The new layer models scheduling and observations around a process. A controller's process model alone cannot establish what a cached read sees, when the key runs again, or how errors schedule retries.

The prototype now runs Quint-generated runtime traces against **the real controller-runtime v0.23.3 worker loop, client-go v0.35.0 FIFO queue and rate limiter, and MAC's actual Observe/Publish methods**. Those methods execute inside a scripted reconciler whose steps and return values are selected by the trace. The parent MAC.Reconcile method also has a targeted stale-cache integration test. Arbitrary Go controllers are not automatically adapted.

## Following the configuration

`system.yaml` is a KRM `System`. Its `spec.runtime` points to `runtime/controller-runtime.yaml`, a KRM `RuntimeProfile`:

```yaml
apiVersion: architecture.local/v1alpha1
kind: RuntimeProfile
metadata:
  name: controller-runtime-demo
spec:
  controllerRuntimeVersion: v0.23.3
  clientGoVersion: v0.35.0
  queue: client-go-fifo
  maxConcurrentReconciles: 1
  key: demo/infra-customer
  cachedReads: [MachineClaim, InfraMachine]
  directReads: [MachineClaim]
  retryDelayTicks: 2
  requeueAfterTicks: 3
  maxTicks: 20
```

**FIFO is an explicit demo choice.** Controller-runtime v0.23.3 defaults to its priority queue. This adapter injects client-go's FIFO queue with a fixed two-tick rate limiter. It rejects other profile settings instead of claiming they have the same semantics. Exact retry timings are application options, not universal runtime behavior. An actual client can bypass caching for selected types; the demo deliberately puts its unstructured reads through a controlled cached reader.

## What is modeled and exercised

| Concern | Model and executed check |
| --- | --- |
| Coalescing | Adding the same key twice produces one queued item. |
| Work in progress | Adding a processing key marks it dirty; Done permits another run. One key is not processed concurrently. |
| Retry | Transient errors and legacy Requeue use the configured rate limiter. |
| Error precedence | A non-nil error takes precedence over RequeueAfter. |
| Delayed retry | Positive RequeueAfter forgets retry history and requests a delay. |
| Terminal error | No retry is requested by the error handler; existing dirty events/timers can still cause another run. |
| Forget | Clears rate-limiter bookkeeping; it does not remove a dirty key or cancel a previously scheduled FIFO timer. |
| Cache lag | API revocation and informer delivery are separate events; cached observation can remain old. |
| Direct read | MAC publication checks the API snapshot against the observed assignment. |
| Stale write | Cached InfraMachine resourceVersion can cause Status.Update to conflict. |
| Per-object versions | Updating MachineClaim does not itself cause an InfraMachine version conflict. |

The five deterministic traces cover successful publication and a stale-write conflict, dirty-key coalescing, retry precedence, stale-cache publication rejection and a timer surviving an intervening successful reconcile. Another 100 sampled traces explore 60 transitions each. The checked-in report records trace counts, coverage, hashes, versions/profile and implementation source receipts. Modified code, profiles, generated models or traces invalidate saved evidence.

A mutation test switches MAC's authorization read from APIReader to the cached reader. The stale-cache trace must then fail conformance at publication. Requirements and expected states come from the model; the adapter receives event names and reads implementation state independently.

Native Go tests additionally use a **real SharedIndexInformer with a fake watch transport**, and a **real client-go delaying queue with a fake clock**. The informer test explicitly selects classic list/watch because its fake transport does not implement streaming WatchList bookmarks. These support the library summaries without pretending to test a real API server or network.

## Composition with process extraction

```text
KRM System -> RuntimeProfile -> runtime semantics -> Quint runtime traces
                                                    |
                           real worker + queue + MAC process replay

Go Publish method -> extracted control flow -> separate fine-grained Quint checks
```

Runtime `publish` is currently a macro-step. It does not expose the read-to-write interval inside Publish. The separately extracted Publication model does expose that interval and still finds the earlier live-lease race. A successful runtime replay therefore does not resolve that race or the prepared-provisioning-call race.

[Process/runtime composition](COMPOSITION.md) now replaces that macro-step in a separate composed model with the extracted program counter and per-invocation snapshots. It reuses this layer's queue semantics and adapter, pauses the actual Go method at client boundaries, and forwards its returned error to the worker. The coarse runtime demo remains a regression baseline. Parent subreconciler ordering and multi-worker interleavings remain outside both profiles.

## Run

```sh
# Go and Quint 0.33.0 on PATH (or use --quint /path/to/quint):
go run ./cmd/arch generate
go run ./cmd/runtime-demo

# Native Go replay of the checked-in runtime evidence and library tests:
go test ./examples/machinevendor/runtimecheck

# Standalone process checks and composed paused-Go replay:
go run ./cmd/behavior-demo
go run ./cmd/composition-demo

# Include the runtime view in the saved LikeC4 site:
go run ./cmd/arch render
```

Inspect `generated/runtime/Runtime.qnt`, `generated/runtime-overview.c4`, `reports/runtime/summary.yaml`, and `examples/machinevendor/runtimecheck`.

This layer checks bounded traces, not exhaustive safety or eventual convergence. Multi-key/multi-worker interleavings, priority scheduling, predicates and owner mapping, cache startup/relist failures, leader changes, deletion/finalizers and fairness are not yet part of the combined model. The profile is authored and version checked; extraction of manager/builder configuration is still future work.

Primary implementation references:

- https://github.com/kubernetes-sigs/controller-runtime/blob/v0.23.3/pkg/internal/controller/controller.go
- https://github.com/kubernetes-sigs/controller-runtime/blob/v0.23.3/pkg/controller/controller.go
- https://github.com/kubernetes-sigs/controller-runtime/blob/v0.23.3/pkg/client/client.go
- https://github.com/kubernetes/client-go/blob/v0.35.0/util/workqueue/queue.go
- https://github.com/kubernetes/client-go/blob/v0.35.0/util/workqueue/rate_limiting_queue.go
- https://github.com/kubernetes/client-go/blob/v0.35.0/tools/cache/shared_informer.go

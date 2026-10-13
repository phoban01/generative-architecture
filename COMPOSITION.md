# Composing process behavior with controller-runtime

The publication control flow extracted from Go now runs inside the runtime model. Lease changes, cache delivery and another writer can occur between individual API operations. Quint schedules are replayed against the unmodified `MACReconciler.Publish` method running inside the real controller-runtime worker.

The key distinction is per-invocation state: the observed assignment, direct lease-read snapshot and InfraMachine resourceVersion survive later changes to the API and cache. The model's source program counter advances through the extracted branches and returns while the shared queue model handles scheduling.

## Wiring

The KRM System selects both the existing runtime profile and the new composition:

```yaml
# Within System.spec:
runtime: runtime/controller-runtime.yaml
composition: behavior/composition.yaml
```

```yaml
apiVersion: architecture.local/v1alpha1
kind: ProcessComposition
metadata:
  name: mac-publication-runtime
spec:
  process: behavior/publication.yaml
  resultPolicy: propagate-error
```

`process` selects the existing BehaviorAnalysis. Its component/action selectors resolve the Go method through source descriptors. The runtime profile supplies the already-tested FIFO semantics; neither the Go method name nor queue options are duplicated in the composition document. The compiler rejects a process for which this prototype has no paused adapter.

`propagate-error` is an explicit caller policy: after Publish returns, pass its actual error to the controller worker with an empty reconcile result. An error triggers the configured retry; `false, nil` completes successfully and needs another event to run again. This policy agrees with the selected MAC process's calling convention, but the complete parent MAC.Reconcile sequence is not extracted here.

## Generated versus supplied

| Part | Origin |
| --- | --- |
| Branch conditions, local write expression, early returns and returned error | Extracted from Go AST |
| Queue coalescing, Done, Forget and retry scheduling | Shared runtime model used by both runtime demos |
| Process selection and caller result policy | KRM configuration |
| Client effects, cache delivery, API faults and per-object version conflicts | Explicit adapter/environment summaries |
| Safety requirements | Existing independently authored CEL in BehaviorAnalysis |

The runtime model's earlier atomic `publish` action remains as a coarse regression baseline. The composed model replaces that action with the extracted process nodes. These models share queue semantics rather than maintaining separate versions of retry rules.

## How the Go replay works

The adapter injects client wrappers into a copy of the reconciler's configuration; it does not modify the production method body. Each publication API call pauses before performing its effect:

1. Direct `MachineClaim` read.
2. Cached `InfraMachine` read.
3. `InfraMachine` status update.

The trace releases each call or injects a failure before its effect. Between calls, the driver can revoke the lease, deliver cache updates, enqueue another event, or write the InfraMachine concurrently. The method's real return value and error are captured; that same error is passed to the real worker and its queue.

Assertions compare actual reads, captured assignment snapshots, submitted write values, API/cache state, return values, queue length and retry state with the model. Resource versions are opaque strings in Go; the adapter checks the equality relationships represented by model revision counters rather than assuming a numeric Kubernetes resourceVersion.

Pure Go branches and local assignments are *stuttering steps*: Go can execute them ahead of the model until it reaches the next paused API boundary. The adapter does not pretend to inspect private Go locals or literal instruction timing. Branch mistakes are detected through the API sequence, submitted writes and returns they produce. Cancellation releases a Go method paused at a boundary, including when a counterexample ends before the method returns.

## Results

The check produces nine deterministic schedules and 100 sampled schedules: **109 traces and 8,360 transitions**. It covers successful publication, stale observation rejection, revocation after reading, cache catch-up, status-write conflicts, a conflict followed by retry and recovery, events arriving while processing, and injected failures at each API boundary.

Two implementation mutations must fail conformance: using a cached authorization reader and bypassing stale-observation revalidation. A separate extraction test changes `return ..., err` to `return ..., nil` and checks that the generated caller behavior changes.

The stronger `publicationUsesLiveLease` property fails in a 14-transition trace. That counterexample also replays against the unmodified Go method:

1. Observe the assigned machine through the cache.
2. Enter Publish and read the live lease.
3. Revoke the lease and deliver the revoked assignment to the cache.
4. Continue with the lease snapshot already held by Publish.
5. Read InfraMachine, set the machine ID and commit its status.

Updating MachineClaim does not invalidate an InfraMachine resourceVersion. Cache catch-up does not replace an in-flight local snapshot. The weaker `publicationUsesCheckedLease` property holds on the sampled schedules, but that does not establish authorization at commit time.

This demonstrates the race in the executed demo under the controlled API/cache environment. It is not evidence that production MAC or CAPI has the same behavior or lacks additional coordination. A successful demo command means the expected conformance results and expected violation were reproduced.

## Run

```sh
# Fresh extraction and composition, without Quint:
make generate

# Fresh composed traces and paused-Go replay, with Quint 0.33.0:
go run ./cmd/composition-demo

# Refresh all layers after implementation changes:
make verify

# Replay saved evidence, including the counterexample, with native Go:
go test ./examples/machinevendor/runtimecheck

# Check wrapper synchronization and cancellation:
go test -race ./examples/machinevendor/runtimecheck

# Rebuild the LikeC4 site, including composed_publication_race:
go run ./cmd/arch render
```

Use `QUINT_BIN` or `--quint /path/to/quint` for the external CLI. Go must be on PATH for source extraction. Models, configuration, source inventories and recorded traces are hash checked; stale evidence fails replay tests.

Inspect `generated/composition/Composed.qnt`, `generated/composition/composition.yaml`, `reports/composition/summary.yaml`, `reports/composition/live-lease-race.yaml` and `generated/composition-race.c4`. The implementation lives in `examples/machinevendor/runtimecheck/composition_*.go` and reuses `examples/machinevendor/behaviorgen` plus the shared queue helper.

## Remaining boundaries

This is one selected process, one key and one worker. The lease abstraction has one machine and one assignment epoch; the standalone contract/process models retain their broader finite domains. API data shapes are fixed, cache delivery is controlled, and injected API errors occur before effects. Ambiguous network outcomes, real API-server behavior, priority queues, multi-worker composition, parent subreconciler ordering and automatic manager wiring remain future work. No exhaustive verification or liveness proof is claimed.

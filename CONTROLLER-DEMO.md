# How generation connects a contract to a controller

Run from the repository root:

```sh
go run ./cmd/arch generate
go run ./cmd/controller-demo
go test ./...
```

Build native binaries with `make build` to run them from `bin/`. The controller demo needs no running cluster, envtest binaries, Node or Quint installation. It replays the existing hash-checked Quint evidence. Use `arch check` with Quint installed to generate fresh traces after a model change.

## Two sides of code generation

The current implementation now extracts structure from Go types and small architecture markers. Resource GVKs come from package/type context; a separate YAML mapping supplies semantic joins. Existing behavioral contracts remain the authority for protocols and expected behavior. See `SOURCE-CODEGEN.md` for the source-side example.

```text
Go types + small markers + source-mappings.yaml
             |
        checked structural descriptor
             |
Authored contracts + system bindings
             |
          arch generate
             +--> Quint -> generated execution traces
             +--> LikeC4 (behavior and source structure views)
             +--> Go ActionDriver interface + Dispatch function
                                |
                  handwritten controller adapter
                                |
                       MAC phase methods
                                |
                  Kubernetes fake client objects
                                |
                    handwritten state projection
                                |
               compare with expected Quint state
```

The generator supplies the action surface and dispatch. It never generates the controller's decision logic or fills its state with expected trace values. Contract guards and updates belong on the expected-behavior side; implementation results come from the independently written controller.

## Follow `mac.publish`

The contract declares `mac.publish(infra)`. Generation produces:

```go
type ActionDriver interface {
    // ...other generated action methods...
    MacPublish(p0 string) (bool, error)
}
```

The generated dispatcher maps the action name and validates its arguments before invoking this method. The adapter implements it by calling:

```go
return a.mac.Publish(a.ctx, key(id), a.observed[id])
```

`observed[id]` is an earlier snapshot returned by the controller's `Observe` method. The trace scheduler can revoke the claim before calling `Publish`. `Publish` independently reads the current MachineClaim, rejects a stale machine ID/epoch, and writes the accepted machine ID to InfraMachine status using `client.Status().Update`.

The projector reads InfraMachine status and MachineClaim status from the fake API client. The generic replay driver compares that projection and the operation's acceptance result to Quint's next state. The adapter never receives the expected state.

Turning on the deliberate `AllowStalePublication` mutation removes the controller's revalidation. The same stale-lease and stale-epoch traces then expose an acceptance/state mismatch. A passing test therefore requires both the correct implementation to agree and the broken implementation to be detected.

The machine service still has no independent lease check. The known prepared-call/revocation safety counterexample remains reproducible; conformance does not imply that every safety question passes.

## What executes

`MACReconciler` implements controller-runtime's `reconcile.Reconciler`. Its normal `Reconcile` composes `EnsureClaim`, `Observe`, `Publish` and `Clear`. A separate lifecycle test invokes the complete `Reconcile` method and checks claim ownership, publication, clearing and semantic idempotence.

Model-based replay calls those **same phase methods directly**, allowing interleavings between observation and publication. It does not drive an actual manager or workqueue. Snapshot state is held by the test scheduler, representing a read suspended between phases. Claim allocation/revocation, InfraMachine call preparation, and the physical machine service are handwritten environment fakes.

The API objects use the illustrative group `demo.architecture.local/v1alpha1`. No actual CAPI types or production MAC repository have been imported. GVKs and field paths are now checked from Go source and package metadata. Semantic joins and ownership are declared in YAML; runtime object lookups and scheduling remain handwritten adapter knowledge.

The adapter uses controller-runtime v0.23.3's fake client with status subresources configured. This runs Kubernetes client operations against an in-memory store. It does not establish API-server validation, watch ordering, cache consistency, conflict/retry behavior, or garbage collection. The publication read and write are also not a cross-object atomic transaction; the current action abstraction does not explore revocation between those two calls. The separate [runtime layer](RUNTIME-CONFORMANCE.md) now tests controlled cache lag, conflicts and retries through a real worker and queue. Its API store and watch transport remain simulated; real API-server conformance still needs envtest.

## Where the files are

All paths below are relative to the repository root:

| File | Responsibility |
|---|---|
| `examples/machinevendor/architecture/adaptergen.go` | Generates interface, argument checks and dispatch from actions |
| `generated/adapter_generated.go` | Generated action interface, dispatcher and model hash |
| `examples/machinevendor/controllerdemo/mac.go` | Independent demo MAC reconciler |
| `examples/machinevendor/controllerdemo/adapter.go` | Handwritten scheduling, field mappings, environment fakes and projection |
| `examples/machinevendor/architecture/trace.go` | Generic `Adapter` interface and `ReplayAdapter` state comparison |
| `examples/machinevendor/controllerdemo/adapter_test.go` | Trace replay, mutation checks and complete reconcile lifecycle test |
| `cmd/controller-demo/main.go` | Standalone demo command |
| `reports/controller-demo.yaml` | Last completed controller-demo evidence |

`arch generate` is a separate command that does not import the controller adapter. After changing contract actions, run generation first; then fix any adapter compile errors. This avoids making the generator depend on an already-updated implementation. A Go test rejects stale generated source, and the demo command also checks the compiled model hash. Changes to implementation alone require rerunning tests; generation cannot establish conformance.

## Extending this to your repositories

For an owned MAC repository, import its actual reconciler and replace these demo-specific field mappings and scheduling seams. Use envtest to exercise real CRDs and API semantics, then model retries and cache observations explicitly. Existing code can stay where it is; the adapter can span packages.

For an unmodified upstream dependency such as CAPI, internal phase hooks may be unavailable. Use an observable boundary contract with coarser actions, drive its real controllers through API changes, and allow intermediate reconciliation steps before checking the projection. Do not force an upstream controller into this demo's one-phase-per-action schedule.

The reusable part is generation, dispatch, evidence checking and trace comparison. The semantic mapping from model actions to controller execution still needs to be supplied and reviewed once for each integration.

References: [controller-gen](https://book.kubebuilder.io/reference/controller-gen.html), [controller-runtime fake client and limitations](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/client/fake), [reconcile interface](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile).

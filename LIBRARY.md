# Using the library

Start with [the source-to-model walkthrough](START-HERE.md). The intended workflow is **implementation → generated system model → independent constraints → counterexamples and implementation replay**. The library supports bounded pieces of this workflow. Arbitrary-system discovery and full Go verification are not implemented.

Public packages live under `pkg/`. Machine-vendor domain semantics remain under `examples/machinevendor/`. The repository is a local, unpublished module named `generative-architecture`. Another module can consume this checkout with:

```go
require generative-architecture v0.0.0
replace generative-architecture => /absolute/path/to/generative-architecture
```

Choose a hosting/module path before publishing. KRM documents use `architecture.local/v1alpha1`; public APIs are experimental.

## Extraction and generation

| Package | Responsibility |
| --- | --- |
| `goflow` | Select a Go function/method, extract supported branches, assignments and returns, and reject unsupported syntax |
| `process` | Lower extracted control flow plus effect summaries into a model; inline explicitly bound, acyclic subreconciler calls |
| `contracts` | Check `+arch:implements=name/version` against Go/JSON field schemas; external selectors support unmodified types |
| `dynamicgen` | Extract selected dynamic-client resource operations; resolve bounded GVR/GVK catalogs; check joins to effect summaries |
| `watchgen` | Extract supported controller-runtime builder registrations, common predicates and local field-reference key mappings |
| `sourcegen` | Extract Go type/GVK/controller markers, record schemas and contract implementation evidence |
| `composition` | Namespace processes, bind shared state through versioned contracts, and connect unambiguous providers automatically |
| `model`, `expr` | Validate typed state/transitions; compile CEL to Quint; generate LikeC4; independently evaluate original CEL |
| `effect` | Identify versioned operations and pause at observable boundaries; actor identities distinguish controllers |
| `controllerruntime` | Observe client calls, schedule real workers, and generate/check the declared FIFO runtime model |
| `conformance` | Decode ITF traces and compare actual implementation observations with independently checked model steps |
| `quint`, `krm` | Invoke bounded Quint simulation and read/write strict KRM YAML |

The core `model`, `expr`, `effect`, `process` and `conformance` packages have no Kubernetes dependency. Source/contract frontends typecheck Go packages. API integration is separate.

`goflow.Extract` accepts scope and representation mappings plus a statement-summary callback. It extracts condition expressions, assignment values and returns from the selected source. Calls must have explicit summaries; it does not infer helper semantics from names. `VerifyDeclarations` can pin a summarized helper body. Use `goflow.Select` to typecheck/select local or dependency code without modifying it.

The supported subset has scalar expressions, explicit record mappings, conditional branches and mapped assignments. Loops, reflection, arbitrary calls, aliasing and goroutine semantics are not inferred. Integer domains must exclude Go overflow unless separately modelled. `process.Inline` resolves a finite call graph with explicit parameter/local/return bindings; retain parent and child source hashes in provenance.

`watchgen.Extract` consumes setup source plus a resolved Go-type/GVK catalog, which can come from the existing source frontend or a checked integration catalog. It records source locations and hashes, primary/secondary registrations, mapper name-field paths and predicates. The caller must typecheck the input package; this frontend validates a bounded syntax pattern. Nonstandard/API imports currently need explicit aliases.

Supported setup is a single fluent `NewControllerManagedBy(mgr).For(...).Watches(...).Complete(r)` return. A local mapper must type-assert the watched object, reject an empty reference, and return one same-namespace key selected by a field path. The selected field comes from Go. Per-watch `builder.WithPredicates` and global `WithEventFilter` support generation-change and resource-version-change predicates. `ExtractWithGVKs` also supports unstructured object declarations followed by `SetGroupVersionKind` with a literal or explicitly bound runtime GVK. Other builder options, `Owns`, custom handlers/predicates, setup branching and arbitrary mapping functions fail extraction. The assignment example lowers these rules into its bounded event/queue model; it does not yet generate a general multi-project watch composition automatically.

`dynamicgen.Extract` reads selected methods containing `Client.Resource(gvr).Namespace(ns).Get/Create/Update/UpdateStatus` calls, including cluster-scoped resources without `Namespace`. A KRM `KubernetesResourceCatalog` binds runtime GVR expressions to exact GVKs, plural names and scopes; it does not fetch API discovery or CRDs yet. Unknown expressions and escaped/aliased clients fail extraction. The output records resource dependencies and source hashes, and `CheckEffects` checks them against declared effect IDs. It does not extract all control flow inside API helpers. The assignment example uses this frontend for its actual `DynamicStore` and generates `dynamic-client.yaml` and `dynamic-client.c4`.

## Contracts and system composition

Owned types can declare:

```go
// +arch:implements=mac.allocation/v1
type InfraMachineStatus struct {
    MachineID string `json:"machineId"`
}
```

The declaration is checked against an `InteractionContract`, such as `contracts/allocation-interface.yaml`. Package context supplies the type identity. This establishes structural compatibility, not business correctness. `contracts.Inspect` also accepts external package/type/field selectors, so upstream types need no annotations.

`composition.Compose` combines any public `model.Spec`, including results of `process.Expand` and explicitly authored upstream abstractions. Each component exposes typed state through ports with a contract, version and direction. Bound inputs alias their provider's state; private snapshots remain distinct. Read-only inputs reject writes. Contract/type/initial-value mismatches, missing bindings and namespace collisions fail validation.

`composition.AutoLink` connects an input when exactly one output provides its contract/version. Explicit links disambiguate multiple providers. This is automatic composition of declared contracts; the supported watch frontend supplies resource/key connections, but automatic extraction of every composition port is still missing.

`composition.Load` reads a KRM `SystemComposition` whose components reference models in local project checkouts. Optional `autoLink: true` enables those contract joins. Projects specify checkout roots and expected Go module paths. The loader records document/model/module hashes; it does not fetch or update repositories. Receipts detect selected-input changes, not every possible transitive runtime dependency.

CEL substitution operates on syntax trees, so renaming a state variable never rewrites a string literal. Comprehension substitution is rejected until capture-safe binding is supported.

## Replay and runtime

A `conformance.Adapter` implements environment actions and projects actual state. It never receives expected state. `Replay` compares the full projection; `ReplayObserved` compares an explicitly selected subset while still validating every model transition and property. Use this when private instruction counters cannot be observed in Go.

Requirements can be compiled separately from the behaviour model. This allows a trace to conform to the implementation while violating a desired constraint, as the larger MAC publication checks demonstrate. The `examples/assignment/` walkthrough now covers the full discovery/contract/claim/fulfillment/status flow, with unchanged constraints detecting source mutations. Changing source must regenerate the behaviour model; it must not silently rewrite the requirements.

`controllerruntime.Group` supports multiple controllers, keys and workers, with one gate per invocation and a shared logical clock. Each controller uses a real controller-runtime worker and native client-go FIFO queue. It keeps native duplicate/dirty-key handling, retry bookkeeping and FIFO dequeue order. `InvocationHook` routes injected client effects to the current invocation; `Observer.Actor` resolves controller-specific resource effects. The older `Worker` API is a one-key convenience wrapper over this engine. `Controller.EnqueueFrom` and `Worker.EnqueueFrom` deliver native event handlers into that same queue, recording submitted keys and rejecting keys outside the declared domain. The hook supports the synchronous `Add` operations used by the selected standard handlers; arbitrary queue callbacks are not a model frontend.

`BuildGroupModel` generates the corresponding scheduling model from a `GroupProfile`. `ReplayGroup` checks sampled schedules with scripted reconcile outcomes, exercising real runtime handling. Business reconciler conformance requires its own injected dependencies and state projection. The profile uses a fixed retry delay and explicit whole-second timers; positive fractional delays round up. It is tested with controller-runtime v0.23.3 and client-go v0.35.0, selecting FIFO explicitly rather than the default priority queue.

The controller-runtime client observer supports Get, Update and status.Update without extra options. Unstructured objects need an explicit GVK but no typed Scheme. `Observer.Dynamic(base, mapper)` additionally supports dynamic-client Create, resolving exact GVRs through a supplied RESTMapper. It validates scope and object identity, preserves API errors, and captures detached request/response snapshots. Dynamic List, Watch transport, Delete, Patch, Apply, other subresources and nonzero options fail closed. Support for those needs corresponding effect semantics and replay coverage. Cache delivery and API conflicts in the examples are controlled fixtures. The assignment walkthrough extracts supported builder/watch syntax and checks selected native predicates and event handlers against the real queue. It does not start a manager or an API server, nor verify actual informer delivery. Its adapter selects native components from the extracted rules; this is not an independent capture of registrations from a running manager. Scheduler counters are bookkeeping, while native dequeue order, ready counts, retry counts, effect results and API contents supply observable checks.

## Run and verify

```sh
go run ./cmd/walkthrough          # source extraction, Go replay, separate constraints
go test ./pkg/... ./examples/assignment
make consumer-test               # imports from a separate Go module
make verify                      # refresh all model evidence and run all tests
```

Fresh simulation requires the external Quint 0.33.0 CLI (`QUINT_BIN`); LikeC4 rendering requires LikeC4 1.59.4 (`LIKEC4_BIN`). The application/library is Go and has no npm project.

`make verify` includes the full MAC walkthrough with 40 sampled traces, the larger MAC process/runtime composition checks, ConfigMirror integration tests, and 40 sampled multi-controller runtime traces. Targeted tests cover concurrent invocations, source mutations, unsupported syntax, subreconciler returns, contract drift, ambiguous providers, shared-state snapshots and read-only ports. Simulation is bounded, not exhaustive verification or a liveness proof.

## Remaining work

Extend the bounded watch frontend to additional builder patterns, ownership references and supported effect summaries, then connect its resource/key identities to general multi-controller composition. Dynamic wiring and arbitrary Go behaviour still require explicit summaries or new frontend support. Integration adapters still establish the observation mapping. Upstream CAPI/cert-manager behavioural abstractions need versioned summaries and independent conformance evidence. API-server-backed tests, additional runtime profiles and publishing/API stability remain future work.

The miniature demonstrates the intended source/requirements separation. ConfigMirror remains a deliberately handwritten behaviour model used to test the runtime integration. Neither example is evidence that arbitrary repositories are already translated automatically.

See [PROJECT-PLAN.md](PROJECT-PLAN.md) for the staged delivery plan.

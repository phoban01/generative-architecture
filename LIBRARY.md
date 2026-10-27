# Using the library

Start with [the source-to-model walkthrough](START-HERE.md). The intended workflow is **implementation → generated system model → independent constraints → counterexamples and implementation replay**. The library supports bounded pieces of this workflow. Arbitrary-system discovery and full Go verification are not implemented.

Public packages live under `pkg/`. Example-specific wiring stays under `examples/`. The repository is a local, unpublished module named `generative-architecture`. Another module can consume this checkout with:

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
| `sourcegen` | Extract Go type/GVK/controller markers, record schemas and contract implementation evidence; a `+kubebuilder:object:root=true` type (other than a list) is a resource without `+arch:resource` |
| `adaptergen` | Generate replay adapter code from the IR and abstraction: projections that read scalar JSON paths through API clients, environment actions and effect-boundary gating; generated code never reads expected model state |
| `composition` | Namespace processes, bind shared state through versioned contracts, and connect unambiguous providers automatically |
| `model`, `expr` | Validate typed state/transitions; compile CEL to Quint; generate LikeC4; independently evaluate original CEL |
| `effect` | Identify versioned operations and pause at observable boundaries; actor identities distinguish controllers |
| `controllerruntime` | Observe client calls, schedule real workers, and generate/check the declared FIFO or priority queue runtime model |
| `conformance` | Decode ITF traces and compare actual implementation observations with independently checked model steps |
| `quint`, `krm` | Invoke bounded Quint simulation and read/write strict KRM YAML |

The core `model`, `expr`, `effect`, `process` and `conformance` packages have no Kubernetes dependency. Source/contract frontends typecheck Go packages. API integration is separate.

`goflow.Extract` accepts scope and representation mappings plus a statement-summary callback. It extracts condition expressions, assignment values and returns from the selected source. Calls must have explicit summaries; it does not infer helper semantics from names. `VerifyDeclarations` can pin a summarized helper body. Use `goflow.Select` to typecheck/select local or dependency code without modifying it.

The supported subset has scalar expressions, explicit record mappings, conditional branches and mapped assignments. Loops, reflection, arbitrary calls, aliasing and goroutine semantics are not inferred. Integer domains must exclude Go overflow unless separately modelled. `process.Inline` resolves a finite call graph with explicit parameter/local/return bindings; retain parent and child source hashes in provenance.

`watchgen.Extract` consumes setup source plus a resolved Go-type/GVK catalog, which can come from the existing source frontend or a checked integration catalog. It records source locations and hashes, primary/secondary registrations, mapper name-field paths and predicates. The caller must typecheck the input package; this frontend validates a bounded syntax pattern. Nonstandard/API imports currently need explicit aliases.

Supported setup is a single fluent `NewControllerManagedBy(mgr).For(...).Watches(...).Complete(r)` return. A local mapper must type-assert the watched object, reject an empty reference, and return one same-namespace key selected by a field path. The selected field comes from Go. Per-watch `builder.WithPredicates` and global `WithEventFilter` support generation-change and resource-version-change predicates. `ExtractWithGVKs` also supports unstructured object declarations followed by `SetGroupVersionKind` with a literal or explicitly bound runtime GVK. Other builder options, `Owns`, custom handlers/predicates, setup branching and arbitrary mapping functions fail extraction. The sample database controller (`examples/database`) lowers these rules into its bounded event/queue model; it does not yet generate a general multi-project watch composition automatically.

`dynamicgen.Extract` reads selected methods containing `Client.Resource(gvr).Namespace(ns).Get/Create/Update/UpdateStatus` calls, including cluster-scoped resources without `Namespace`. A KRM `KubernetesResourceCatalog` binds runtime GVR expressions to exact GVKs, plural names and scopes; it does not fetch API discovery or CRDs yet. Unknown expressions and escaped/aliased clients fail extraction. The output records resource dependencies and source hashes, and `CheckEffects` checks them against declared effect IDs. It does not extract all control flow inside API helpers. The sample controller uses this frontend for its actual `DynamicStore` and generates `dynamic-client.yaml` and `dynamic-client.c4`.

## Contracts and system composition

Owned types can declare:

```go
// +arch:implements=database.endpoint/v1
type DatabaseStatus struct {
    Endpoint string `json:"endpoint"`
}
```

The declaration is checked against an `InteractionContract` document. Package context supplies the type identity. This establishes structural compatibility, not business correctness. `contracts.Inspect` also accepts external package/type/field selectors, so upstream types need no annotations.

`composition.Compose` combines any public `model.Spec`, including results of `process.Expand` and explicitly authored upstream abstractions. Each component exposes typed state through ports with a contract, version and direction. Bound inputs alias their provider's state; private snapshots remain distinct. Read-only inputs reject writes. Contract/type/initial-value mismatches, missing bindings and namespace collisions fail validation.

`composition.AutoLink` connects an input when exactly one output provides its contract/version. Explicit links disambiguate multiple providers. This is automatic composition of declared contracts; the supported watch frontend supplies resource/key connections, but automatic extraction of every composition port is still missing.

`composition.Load` reads a KRM `SystemComposition` whose components reference models in local project checkouts. Optional `autoLink: true` enables those contract joins. Projects specify checkout roots and expected Go module paths. The loader records document/model/module hashes; it does not fetch or update repositories. Receipts detect selected-input changes, not every possible transitive runtime dependency.

CEL substitution operates on syntax trees, so renaming a state variable never rewrites a string literal. Comprehension substitution is rejected until capture-safe binding is supported.

## Replay and runtime

A `conformance.Adapter` implements environment actions and projects actual state. It never receives expected state. `Replay` compares the full projection; `ReplayObserved` compares an explicitly selected subset while still validating every model transition and property. Use this when private instruction counters cannot be observed in Go.

Requirements can be compiled separately from the behaviour model. This allows a trace to conform to the implementation while violating a desired constraint. The `examples/database/` walkthrough covers the full discovery/contract/instance/provisioning/status flow, with unchanged constraints detecting source mutations. Changing source must regenerate the behaviour model; it must not silently rewrite the requirements.

`controllerruntime.Group` supports multiple controllers, keys and workers, with one gate per invocation and a shared logical clock. Each controller uses a real controller-runtime worker and a native queue: client-go's FIFO queue by default, or controller-runtime's priority queue when `ControllerOptions.Queue` is `priority`. It keeps native duplicate/dirty-key handling, retry bookkeeping and dequeue order. The priority queue keeps its own wall clock, so the group parks its delayed items and makes them ready through the native timer path when the logical clock fires them. `InvocationHook` routes injected client effects to the current invocation; `Observer.Actor` resolves controller-specific resource effects. The older `Worker` API is a one-key convenience wrapper over this engine. `Controller.EnqueueFrom` and `Worker.EnqueueFrom` deliver native event handlers into that same queue, recording submitted keys and rejecting keys outside the declared domain. The hook supports the synchronous `Add` operations used by the selected standard handlers; arbitrary queue callbacks are not a model frontend.

`BuildGroupModel` generates the corresponding scheduling model from a `GroupProfile`. `ReplayGroup` checks sampled schedules with scripted reconcile outcomes, exercising real runtime handling. Business reconciler conformance requires its own injected dependencies and state projection. The profile uses a fixed retry delay and explicit whole-second timers; positive fractional delays round up. It is tested with controller-runtime v0.23.3 and client-go v0.35.0. `GroupProfile.Queue` selects `fifo` (the default) or `priority`; both profiles replay against the native queue. The priority profile follows the pinned priority queue: priorities, `LowPriority` for initial-list events, counters taken at add time, and retries at the priority the key was taken with. A controller with `startup: true` models the initial list and cache sync: `<controller>_<key>_list` adds a key before sync, `<controller>_sync` marks the caches synced, and no key starts before sync. The native group gives that controller a syncing source, so controller-runtime itself starts no worker before `Controller.Sync`. A controller with `deletion: true` adds `<controller>_<key>_tombstone`, which marks the key deleted once and enqueues it again.

The controller-runtime client observer supports Get and Delete without extra options, and DeleteAllOf with a namespace option. Create, Update, status.Update, Patch and status.Patch accept a field owner and record it as the `fieldManager` option. Apply records the `apply` operation with its required field manager and the `force` option. List records the item kind at the reader's consistency, keyed by the namespace option; it accepts an equality label selector and records it as the `labelSelector` option. Patch and status.Patch record the patch type and bytes in `Call.Options`; a server-side apply patch records the `apply` or `status.apply` operation. A dry run fails with an error. Unstructured objects need an explicit GVK but no typed Scheme. `Observer.Dynamic(base, mapper)` supports Get, Create, Update, UpdateStatus and Delete on the dynamic client, List with an equality label selector, and Patch, Apply and ApplyStatus with a field manager and force, resolving exact GVRs through a supplied RESTMapper. It validates scope and object identity, preserves API errors, and captures detached request/response snapshots. Dynamic Watch transport, DeleteCollection, subresources other than status and nonzero options fail closed. Support for those needs corresponding effect semantics and replay coverage. Cache delivery and API conflicts in the examples are controlled fixtures. The sample walkthrough extracts supported builder/watch syntax and checks selected native predicates and event handlers against the real queue. It does not start a manager or an API server, nor verify actual informer delivery. Its adapter selects native components from the extracted rules; this is not an independent capture of registrations from a running manager. Scheduler counters are bookkeeping, while native dequeue order, ready counts, retry counts, effect results and API contents supply observable checks.

## Run and verify

```sh
go run ./cmd/walkthrough          # source extraction, Go replay, separate constraints
go test ./pkg/... ./examples/database
make consumer-test               # imports from a separate Go module
make verify                      # refresh all model evidence and run all tests
```

Fresh simulation requires an external Quint CLI in the supported range (`QUINT_BIN`); LikeC4 rendering requires a LikeC4 CLI in its range (`LIKEC4_BIN`). The ranges are listed in the README's [Supported toolchain](README.md#supported-toolchain) table. The application/library is Go and imports no npm code; `devbox shell` installs both CLIs from `hack/tools/package.json`.

`make verify` includes the sample controller walkthrough with 40 sampled traces, ConfigMirror's 40 traces sampled from its generated model with its mutation suite and receipt, and 40 sampled multi-controller runtime traces. Targeted tests cover concurrent invocations, source mutations, unsupported syntax, subreconciler returns, contract drift, ambiguous providers, shared-state snapshots and read-only ports. Simulation is bounded, not exhaustive verification or a liveness proof.

## Remaining work

Extend the bounded watch frontend to additional builder patterns, ownership references and supported effect summaries, then connect its resource/key identities to general multi-controller composition. Dynamic wiring and arbitrary Go behaviour still require explicit summaries or new frontend support. Integration adapters still establish the observation mapping. Upstream project behavioural abstractions (Cluster API, Flux) need versioned summaries and independent conformance evidence. API-server-backed tests, additional runtime profiles and publishing/API stability remain future work.

The sample controller demonstrates the intended source/requirements separation. ConfigMirror is the first source-generated controller: typed extraction of its reconciler, its abstraction document and the summary library generate its model, replay projection, environment and boundary gating; Quint traces of the model are replayed against the controller, checked against a mutation suite and recorded in a receipt. Its fixtures, client wiring and runtime event routing are still written by hand. Neither example is evidence that arbitrary repositories are already translated automatically.

See [PROJECT-PLAN.md](PROJECT-PLAN.md) for the staged delivery plan.

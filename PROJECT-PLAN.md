# Project plan: generated architecture for Kubernetes systems

Status: draft 3 (2026-10-13). Supersedes the pilot-oriented plan kept in git history. Draft 3 incorporates the review in `docs/reviews/2026-10-13-plan-review.md`.

## 1. Product definition

A Go library and CLI that **generates architecture artifacts for a system powered by Kubernetes**. The system is a set of controller-runtime projects, owned and upstream, that coordinate primarily through the Kubernetes API. Operations outside the API (provisioning calls, cloud SDKs, webhooks) stay in scope as explicit effects with declared boundary semantics; their internals are not extracted. The toolchain:

1. **Captures** controllers, resources, watches, client effects, external effects and reconcile behaviour from Go source and Kubernetes schemas into one shared, versioned intermediate representation (IR).
2. **Abstracts** the captured facts into a bounded executable state: which object fields and local values enter the model, how populations and value domains are bounded, how identity and references are preserved across resources and clusters, and how real API state projects onto that state during replay. Bounds and exclusions are declared inputs and appear in every report.
3. **Composes** those projects into a system model, joining them by resource identity, watches and references.
4. **Generates** executable models (Quint) and diagrams (LikeC4) from the IR.
5. **Checks** independently authored invariants, structural contracts and behavioural protocol contracts against the generated model.
6. **Replays** model executions and counterexamples against the real controllers for conformance evidence, reading implementation state independently of the model.
7. **Reports** support coverage, evidence and architecture diffs in CI.

The architecture follows the implementation. Human input is limited to: selecting projects and bounds, writing constraints and contracts, describing systems that cannot be extracted (boundary models), and resolving ambiguity. Adopting the toolchain must not require restructuring a controller.

### Non-goals

- Proving arbitrary Go programs correct. A documented, fail-closed Go and controller-runtime subset is extracted; everything else is reported as unsupported or summarized with provenance.
- Replacing Kubernetes testing. envtest and clusters supply evidence for behaviour the model cannot establish.
- A domain-specific product. No business domain appears in the library, CLI or reference system.

## 2. Principles

| Principle | Meaning |
| --- | --- |
| Generated, not described | Relationships and behaviour come from code and schemas. A hand-maintained second description of a controller is a defect. |
| Explicit abstraction | The step from Kubernetes objects to bounded model state is a declared, reviewable input (`Abstraction` document), not an implicit choice inside an adapter. What an abstraction preserves, excludes or over-approximates is recorded with the result. |
| Connectivity is not behaviour | Binding a consumer port to a compatible provider establishes connectivity only. Protocol semantics (preconditions, permitted transitions, ordering) are checked separately and never inferred from a successful binding. |
| Independent constraints | Invariants, contracts and assumptions are authored separately and never rewritten by extraction. |
| Evidence kinds | Every fact in the IR is `extracted`, `observed`, `declared`, `assumed` or `unresolved`. A passing run never upgrades a declaration to an extraction. |
| Fail closed | Unsupported syntax, unknown resources, ambiguous joins and version mismatches produce precise diagnostics, never silent omissions. |
| Bounded and honest | Finite domains and sampled exploration are stated in every report. No report claims exhaustive verification or liveness without a model-checking run and explicit fairness. |
| Pinned inputs | Repositories, modules, CRDs, discovery snapshots, tool versions and generator versions are pinned and hashed into receipts. |

## 3. Users and workflows

| User | Workflow |
| --- | --- |
| Platform architect | `arch init` a system repository; pin projects; run `arch extract`, `arch generate`, `arch render`; review diagrams and the support report; author constraints and boundary models. |
| Controller author | Add minimal markers only where discovery is ambiguous; run `arch support` locally; see which constructs are unsupported before CI does. |
| CI | `arch extract → generate → check → replay → diff`; publish diagrams, evidence receipts and the semantic architecture diff as artifacts; fail on constraint violations or invalidated evidence. |

## 4. Requirements index

Each requirement has an ID, an acceptance test and a status measured against the current code. Issues on GitHub reference these IDs. Status values: **Met**, **Partial**, **Missing**.

### ARCH: capture architecture from code

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| ARCH-1 | Discover reconcilers, setup functions and owned API types from a Go module using type information, with no per-controller Go integration code. | `arch extract` on `examples/refsystem` lists every controller without `+arch:controller` markers. | Partial. `pkg/sourcegen` uses `go/packages` but requires markers; `pkg/goflow` matches statements by source text. |
| ARCH-2 | Build a resource catalog (GVK, GVR, scope, served versions) from Scheme registrations, CRD manifests and discovery snapshots. Plural names are never guessed. | Catalog for the reference system is loaded from its CRD YAML with no hand-written bindings. | Partial. `KubernetesResourceCatalog` binds expressions by hand. |
| ARCH-3 | Represent cluster, GVK, namespace/name and UID as distinct identity parts; make served-version conversion explicit. | Two objects with the same GVK and name in different clusters are distinct IR objects. | Missing. |
| ARCH-4 | Extract watch registrations: `For`, `Owns`, `Watches`, `WatchesRawSource`, `Named`, `WithOptions`, `WithEventFilter`, standard handlers and predicates including `And`/`Or`/`Not`. Unsupported forms produce a `file:line` diagnostic. | Reference-system setup functions extract with zero diagnostics; a custom `predicate.Funcs` yields one located diagnostic. | Partial. `pkg/watchgen` supports `For`, `Owns`, `Watches` with a package-level map func or `EnqueueRequestForOwner`, and the generation and resourceVersion predicates with `And`/`Or`/`Not`. `pkg/watchmodel` generates event and delivery state from the IR rules (#58 to #64) and the database sample's hand-written watch model is deleted. Map functions that are methods or list objects are unsupported. |
| ARCH-5 | Extract client effects for typed, unstructured and dynamic clients: Get, List, Create, Update, Patch, Delete, DeleteAllOf, Apply, Status and subresources, with options and read consistency. | Every client call in the reference system maps to an IR effect with operation, resource, consistency and options. | Partial. The typed observer records Get, List, Create, Update, Patch, Apply, Delete and DeleteAllOf, the status subresource for Update, Patch and Apply, and the FieldOwner option; the dynamic observer records Get, List, Create, Delete, Patch, Apply and ApplyStatus (#39 to #45). DryRun is refused. Typed status Apply and dynamic DeleteCollection are not recorded; the reference system's client calls are not yet checked end to end. |
| ARCH-6 | Extract reconcile control flow: branches, early returns, error idioms (`IsNotFound`, `IsConflict`), `switch`, bounded iteration, helper calls resolved by symbol, inlined subreconcilers. | Reference-system reconcilers extract with no statement-text mapping callbacks. | Partial. Branches, assignments and returns only; calls matched by text. |
| ARCH-7 | Ship a built-in effect summary library for the controller-runtime and client-go client surface. Business summaries are exceptional, pinned to a source hash and listed in the support report. | ConfigMirror's behaviour is generated from the summary library, not hand-written. | Partial. `pkg/summaries` ships Get, Create, Update, Delete, Patch, Apply and List; ConfigMirror's model is generated from it (Checkpoint A, ADR-0007). Not yet listed in the support report. |
| ARCH-8 | One shared, versioned IR with stable IDs, source locations, provenance and evidence kind for every fact. All frontends and generators use it. | No generator reads frontend-specific structs; the IR schema has a version and migration policy. | Missing. Five ad hoc result types exist. |
| ARCH-9 | Model controller-runtime scheduling per pinned version: FIFO and priority queues, cache delivery, deduplication, dirty keys, retries, `RequeueAfter`, startup sync and deletion. | Both queue profiles replay against the native queue; a controller using the default priority queue is modelled without configuration. | Partial. Both queue profiles are modelled after controller-runtime v0.23.3 and replay against the native FIFO and priority queues; startup list-and-sync and deletion tombstone events are opt-in per controller (#55 to #57, #65). FIFO is still the default, the native handler path does not carry `LowPriority`, and `cmd/general-demo` replays FIFO only. |
| ARCH-10 | Compose projects into a system by deriving ports and joins from reads, writes, watches and references, cluster-aware. Ambiguous joins require one explicit binding. | Reference-system projects compose from a `Project` document with no hand-written port list. | Partial. Ports are declared; auto-link joins unique providers. |
| ARCH-11 | Generation is deterministic and incremental; a semantic diff between two IRs reports added, removed and changed controllers, watches, effects and assumptions. | `arch diff` on two commits prints the changed watch. | Partial. Outputs are deterministic; `ir.Diff` and `arch diff` report added, removed and changed records by stable ID (#104). A changed watch mapping still shows as removed plus added because the watch ID includes the field path. |
| ARCH-12 | State abstraction is explicit: an `Abstraction` document selects the object fields and locals that enter the model, bounds populations and value domains, declares identity keys and reference fields, and drives both model generation and replay projection. Preserved, excluded and over-approximated behaviours are listed in the report. | ConfigMirror's model state and its replay projection are generated from one abstraction document; the report lists its bounds. | Done for ConfigMirror and the sample database controller (Checkpoint A, ADR-0007, #141): both models and both replay projections are generated from their abstraction documents. Map keys outside annotations and labels are not expressible (#138). |
| ARCH-13 | Operations outside the Kubernetes API are IR effects of kind `external`, identified by callee symbol, with declared outcomes and ordering (evidence `declared` or `assumed`); their internals are not extracted. | A provisioning-style call in a reconciler appears as an external effect with two declared outcomes and is gated during replay. | Missing. |

### VIS: visualize the architecture

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| VIS-1 | Generate LikeC4 from the IR: system context, component, resource/watch, process, runtime and execution (counterexample) views. | `arch generate` writes one LikeC4 workspace covering all six views for the reference system. | Partial. `pkg/likec4` writes one workspace with one shared specification and the context, resource/watch, process, runtime and execution views (#88 to #93). No component view yet; the older example emitters still carry their own specifications (#96). |
| VIS-2 | One command renders and serves every view of a project. | `arch render && arch serve` shows all views. | Partial. `make render` covers the sample controller only. |
| VIS-3 | Presentation hints (titles, grouping, layout) are authored in KRM and never add or remove relationships. | A hint referencing an unknown element fails validation; hints change layout only. | Missing. |
| VIS-4 | Every diagram element and relationship carries its evidence kind and a source link. | Hovering any edge shows `extracted`/`declared`/`assumed` and `file:line`. | Partial. |

### CON: constraints, invariants and contracts

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| CON-1 | Constraints are authored independently as KRM/CEL and never rewritten by extraction. | Mutating source changes the model, not the constraint files. | Met. |
| CON-2 | Safety invariants are expressed over composed system state and events using stable identities, not process node indices. | A constraint referencing an effect survives a source edit that renumbers nodes. | Partial. Events are `node<N>_<kind>`. |
| CON-3 | Structural contracts (`InteractionContract`) are checked against Go types in owned or upstream modules via markers or external selectors. | An upstream type from the module cache is checked without annotation. | Met. |
| CON-4 | Port compatibility and binding: ports carry a contract name, version, type and direction; unique providers join automatically; mismatches and ambiguity fail. This establishes connectivity only. | Two providers of one contract fail until bound explicitly. | Met. |
| CON-5 | The CEL subset is documented and extended (`exists`, `has`, lists, nested records) only with a sound Quint translation and a differential check. | Each new construct has a CEL-versus-Quint differential test. | Partial. |
| CON-6 | Temporal properties (eventually, leads-to) are supported with explicit fairness assumptions, or the report states they are unchecked. | A liveness property either runs under `quint verify` with stated fairness or is reported as unchecked. | Missing. |
| CON-7 | Counterexamples are minimized, source-linked and replayed against Go. | A violation report lists the shortest trace found, each step's source location and the replay result. | Partial. |
| CON-8 | The mutation suite is data: every constraint has at least one mutation it detects. | `arch check --mutations` reports a detection matrix. | Partial. Both examples express their mutations as `MutationSuite` documents run by `pkg/mutation` (#85); the database suite reports a six-row detection matrix. Four database constraints have no mutation yet, and `arch check --mutations` does not exist. |
| CON-9 | Every generated model is checked by evaluating the original CEL against Quint traces. | Differential check runs for any `TransitionModel`, not one example. | Partial. |
| CON-10 | Behavioural protocol contracts: a versioned `ProtocolContract` declares the roles on a binding, preconditions, permitted transitions and independently authored properties; conformance to it is reported separately from binding. Auto-linking is never evidence of protocol conformance. | A bound link whose consumer transitions out of order violates its protocol property. | Partial. The legacy `Contract` schema has guards and updates; `TransitionModel` bindings have none. |

### ADOPT: adoption by any controller-runtime project

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| ADOPT-1 | The supported subset needs zero per-project Go integration code. Inputs are markers, one `Project` document, a catalog source and constraints. | The reference system has no `example.go`, `adapter.go` or `watches.go` equivalents. | Partial. ConfigMirror still carries project Go code: `generate.go` (extraction options and runtime settings), `evidence.go` and an `adapter.go` with fixtures and worker-event routing (ADR-0007). These move into the `Project` document and `cmd/arch` in M7. |
| ADOPT-2 | The marker set is minimal; kubebuilder markers are reused; markers are needed only for ambiguity. | `+kubebuilder:object:root=true` types are resources without `+arch:resource`. | Partial. |
| ADOPT-3 | Replay adapters (client instrumentation, key routing, projection, boundary gating) are generated from the IR, catalog and abstraction document. Generated adapters read implementation state only; they never derive observations from expected model state. | `examples/database` runs with its handwritten adapter deleted; a projection test fails if it reads model state. | Partial. ConfigMirror's projection, environment and gating are generated with an import-restriction test; worker-event routing and fixtures remain handwritten. The database sample's projection is generated (ADR-0007). |
| ADOPT-4 | A support report lists included, summarized and unsupported constructs with locations. | `arch support` output for the reference system is empty of unsupported items. | Missing. |
| ADOPT-5 | One CLI: `init`, `extract`, `generate`, `render`, `serve`, `check`, `replay`, `diff`, `support`. | `arch` imports nothing under `examples/`. | Missing. |
| ADOPT-6 | The library is embeddable and published under a stable module path with a compatibility policy. | `go get github.com/phoban01/generative-architecture@v0.1.0` works. | Partial. |
| ADOPT-7 | External tools are version ranges, not exact pins. | Quint 0.33.x and later patch releases are accepted. | Missing. |
| ADOPT-8 | Two execution environments: the fast fake harness and envtest. Local runs may skip envtest when assets are absent; the CI integration job installs assets and fails if it cannot run them. | The envtest job runs in CI and fails on missing assets; local tests skip. | Partial. Fake only. |
| ADOPT-9 | A neutral multi-controller reference system is exercised end to end by the CLI; domain examples become regression fixtures. | `examples/refsystem` is the walkthrough in README. | Missing. |

### EXT: external and upstream projects

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| EXT-1 | A `BoundaryModel` document describes an unmodified upstream project: pinned version, contracts, assumptions with evidence kind, and a conformance hook. | A boundary model with an `assumed` fact appears in diagrams as assumed. | Missing. |
| EXT-2 | Source in dependency modules can be selected from the module cache without annotations. | A selector for a type in `k8s.io/api` resolves and hashes its source. | Partial. |
| EXT-3 | Cluster API: provider contracts ship as `InteractionContract`s; infrastructure and bootstrap providers extract through the standard frontends; the multi-cluster cache is a boundary. | The InfrastructureMachine contract checks a provider type from the module cache. | Missing. |
| EXT-4 | Flux: source, kustomize and helm controllers have boundary models; server-side apply and patch effects, `interval` and `dependsOn` are modelled. | A system containing a Flux `Kustomization` composes with an owned controller that watches its outputs. | Missing. |
| EXT-5 | No implicit fetching; every input is pinned and receipted. | Receipts list every file and module hash used. | Met. |

### EVID: evidence and CI

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| EVID-1 | Evidence receipts are immutable and invalidated when any input changes. | Editing a watched file fails replay until regenerated. | Partial. Selected inputs are hashed and checked; the full input closure is not. |
| EVID-2 | Reports distinguish safety violations, conformance mismatches, unsupported constructs and tool failures. | Each failure has a category field. | Partial. |
| EVID-3 | Inventory mode reports unresolved facts; verification mode refuses incomplete semantics for the selected scope. | `arch check --strict` fails on an `unresolved` fact in scope. | Missing. |
| EVID-4 | A CI template runs the pipeline and publishes diagrams, receipts and the diff. | The template runs green on the reference system. | Missing. |
| EVID-5 | Receipts cover the consumed input closure (transitive source of selected packages, schemas, configuration, abstraction, boundary assumptions) and record the exact resolved tool versions. Compatibility (version ranges) and reproducibility (exact versions in receipts) are separate concerns. | A receipt lists every file consumed and the exact Quint and LikeC4 versions; changing a transitive dependency file invalidates it. | Missing. |

## 5. Target architecture

```text
Project document (KRM)         CRDs / discovery / Scheme        Pinned modules
        |                               |                            |
        +-------------- Frontends: sourcegen, watchgen, effectgen, flowgen --------+
                                        |
                             Shared IR (pkg/ir) + provenance + evidence kinds
                                        |
                                        |
                            Abstraction (pkg/abstraction)
                 (selected fields, bounds, identity keys, projection spec)
                                        |
                 +----------------------+----------------------+
                 |                      |                      |
          Composition            Summary library         Boundary models
   (ports, joins, protocols)   (client + external)      (upstream, assumed)
                 |                      |                      |
                 +----------------------+----------------------+
                                        |
                              System model (TransitionModel)
                            /            |              \
                      LikeC4          Quint          Generated adapters
                    (6 views)    (sample / verify)   (instrument, route, project)
                                        |                     |
                                 Counterexamples  ---->  Go replay (fake / envtest)
                                        |
                        Reports: support, evidence, diff, categories
```

Package map (target):

| Package | Responsibility |
| --- | --- |
| `pkg/ir` | Identity, records, provenance, evidence kinds, schema version |
| `pkg/catalog` | Resource catalog from CRDs, discovery and Scheme |
| `pkg/sourcegen`, `pkg/watchgen`, `pkg/effectgen`, `pkg/flowgen` | Typed frontends emitting IR (`goflow` and `dynamicgen` are folded in) |
| `pkg/abstraction` | `Abstraction` document: selected fields, bounds, identity keys; drives model state and generated projection |
| `pkg/summaries` | Built-in effect semantics for client operations and declared external effects |
| `pkg/protocol` | `ProtocolContract`: behavioural semantics of a binding, checked separately from port compatibility |
| `pkg/composition`, `pkg/boundary` | System composition and upstream boundary models |
| `pkg/model`, `pkg/expr`, `pkg/process` | TransitionModel, CEL subset, process lowering |
| `pkg/controllerruntime`, `pkg/adaptergen`, `pkg/conformance` | Runtime profiles, generated adapters, replay |
| `pkg/likec4`, `pkg/quint`, `pkg/report` | Generators and reports |
| `cmd/arch` | The CLI (created in M0; the prototype's example-bound CLI was removed) |
| `examples/refsystem` | Neutral reference system: three owned controllers and one Flux boundary |

## 6. Milestones and exit criteria

Milestones group related work. Delivery, however, is driven by **integration checkpoints** that take one existing controller through the complete pipeline before the extraction, composition and diagram surface is expanded. Each checkpoint is a test in the repository, and each GitHub issue carries its milestone and requirement IDs.

### Integration checkpoints

| Checkpoint | When | What it proves | Exit criterion |
| --- | --- | --- | --- |
| **A: first complete flow** | After the minimal M1 IR, the typed-frontend subset of M2 (identifiers, calls, Get/Update summaries) and the narrow adapter-generation slice pulled forward from M5 | Existing source → generated behaviour, effects and watches → bounded model from an abstraction document → Quint exploration → generated replay integration → conformance result, including dynamic-client access and a detected behavioural regression | `examples/configmirror` runs with its handwritten model and adapter deleted; the wrong-write mutation is detected; the receipt lists bounds and exact tool versions |
| **B: external adoption** | After Checkpoint A and the M2 builder support needed by the chosen controller | A controller that was not written for the extractor is pinned, extracted, modelled and either replayed or reported as unsupported with located diagnostics | A pinned upstream controller under `testdata/external/` has a committed support report and generated model; the adoption guide was validated against it |
| **C: release** | End of M7 | The whole surface on the reference system | Tag v0.1.0 |

Checkpoint A is the recommended first delivery. It validates the abstraction and adapter interfaces before the rest of the backlog depends on them. Supported construction and dependency-injection patterns for controllers (how a reconciler receives its client, reader and recorder) are documented at Checkpoint A and extraction fails closed on others; project-specific setup must not be hidden in library helpers.

### Milestones

| Milestone | Scope | Exit criterion | Requirements |
| --- | --- | --- | --- |
| **M0 Foundations** | Public repo, CI, generic CLI namespace, structured diagnostics, toolchain ranges, ADRs, reference-system skeleton | CI green on GitHub; `cmd/arch` imports nothing from `examples/`; `arch support` runs on `examples/refsystem` | ADOPT-5, ADOPT-7, EVID-2 |
| **M1 Shared IR and catalog** | `pkg/ir`, `pkg/catalog`, `pkg/abstraction`, provenance, evidence kinds; existing frontends emit IR | All frontends return IR records; the reference-system catalog loads from CRD YAML; an abstraction document validates | ARCH-2, ARCH-3, ARCH-8, ARCH-12 |
| **M2 Typed frontends and summaries** | Type-resolved control flow, effects, external effects and watches; summary library; marker reduction | Checkpoint A passes; reference-system reconcilers extract with no statement-text callbacks | ARCH-1, ARCH-4, ARCH-5, ARCH-6, ARCH-7, ARCH-13, ADOPT-2, EXT-2 |
| **M3 Watch and runtime composition** | Event-to-key graph from IR; priority-queue profile; startup and deletion | `examples/database/watches.go` is deleted; a priority-queue controller replays | ARCH-9, CON-2 |
| **M4 System composition and boundaries** | Derived ports and joins; `ProtocolContract`; `BoundaryModel`; Cluster API contracts; Flux boundary | Reference system plus Flux boundary composes from one `Project` document; a protocol violation is reported separately from binding | ARCH-10, CON-10, EXT-1, EXT-3, EXT-4 |
| **M5 Generated adapters and evidence** | `adaptergen` beyond the Checkpoint A slice; external-effect gating; envtest harness; report categories; input-closure receipts; mutation registry; minimization | `examples/database` handwritten adapter deleted; envtest CI job runs and fails without assets; detection matrix printed; receipts list the input closure | ADOPT-3, ADOPT-8, CON-7, CON-8, CON-9, EVID-2, EVID-3, EVID-5 |
| **M6 Visualization** | LikeC4 workspace from IR; six views; hints; evidence labels | `arch render` builds every view of the reference system | VIS-1 to VIS-4 |
| **M7 CLI, CI and release** | Full CLI, diff, strict mode, CI template, module path, v0.1.0, adoption guides | Tag v0.1.0; adoption guide validated on an external controller | ARCH-11, ADOPT-1, ADOPT-4, ADOPT-5, ADOPT-6, ADOPT-9, CON-5, CON-6, EVID-4 |

## 7. Work breakdown

The backlog lives in GitHub issues. Every issue is sized for an autonomous agent: one package, one behaviour, one test. That sizing is a unit of work, not an estimate of integration difficulty; package tests can pass while the assembled pipeline models the wrong behaviour. The checkpoint issues (`[CP-A-<n>]`, `[CP-B-<n>]`) exercise the complete pipeline and gate expansion of the surface. The full list is a roadmap: adoption is learned at the checkpoints, not after every issue closes. Issue titles carry a key `[M<milestone>-<n>]` or `[CP-<x>-<n>]`, labels carry the area and requirement IDs, and bodies list dependencies by issue number.

Counts at plan time:

| Milestone | Issues |
| --- | --- |
| M0 | 13 |
| M1 | 12 |
| M2 | 32 |
| M3 | 11 |
| M4 | 13 |
| M5 | 14 |
| M6 | 9 |
| M7 | 16 |
| Checkpoint A | 10 |
| Checkpoint B | 4 |

## 8. Risks

| Risk | Mitigation |
| --- | --- |
| The model repeats its own assumptions | Evidence kinds on every fact; constraints authored separately; mutation registry; envtest evidence |
| Extraction silently misses behaviour | Typed analysis; fail-closed diagnostics; support report; strict mode in CI |
| Real projects use patterns outside the subset | Support report drives the frontend backlog; summary library absorbs standard helpers; boundary models absorb the rest with `assumed` evidence |
| Models become too large to explore | Slice by property and subsystem; finite identities; compose incrementally; `quint verify` only on slices |
| Runtime semantics drift from controller-runtime | Profiles pinned per version; native queue replay in tests; priority queue modelled because it is the default |
| Upstream internals resist instrumentation | Boundary models; module-cache selectors; envtest against pinned releases |
| Tool pins break adopters | Version ranges; CI matrix for Go, Quint, LikeC4 |
| Example-specific seams freeze into the public API | Reference system replaces domain examples before v0.1.0; domain examples kept only as fixtures |
| Package tests pass while the assembled pipeline is wrong | Integration checkpoints A and B run the complete flow on real controllers before the surface expands |
| The abstraction hides behaviour | Abstraction documents list preserved, excluded and over-approximated behaviours; reports repeat the bounds; a bounded pass is never reported as unqualified |

## 9. Decisions

Architectural decisions are recorded in `docs/adr/`. Decisions taken by this plan:

- ADR-0001: `TransitionModel` is the canonical model schema; the prototype's legacy `Contract`/`System` schema was removed.
- ADR-0002: One shared IR (`pkg/ir`) is the only input to generators; frontends never talk to generators directly.
- ADR-0003: External tools (Quint, LikeC4) stay external and are invoked by version range; the Go module imports no npm code (`hack/tools/` only pins the CLIs for devbox).
- ADR-0004: A neutral reference system under `examples/refsystem` is the walkthrough and the source of exit criteria.
- ADR-0005: State abstraction is a declared input (`Abstraction` document) that drives both model generation and replay projection.
- ADR-0006: Integration checkpoints on existing controllers gate expansion of the extraction, composition and diagram surface.
- ADR-0007: Checkpoint A findings: abstraction gaps, adapter interface changes and the receipt drift-scope decision.

## 10. Glossary

| Term | Meaning |
| --- | --- |
| Project | A Go module containing one or more controller-runtime controllers, selected by a `Project` document |
| System | The composition of several projects and boundary models sharing Kubernetes APIs |
| IR | The shared intermediate representation of extracted and declared facts |
| Effect | An observable API operation with resource, operation, consistency and options |
| Summary | Declared semantics for an operation or helper, with provenance |
| Boundary model | A declared model of an upstream project that is not extracted |
| Abstraction | The declared selection of fields, bounds and identity keys that turns Kubernetes objects into model state and back |
| Protocol contract | The behavioural semantics of a binding: preconditions, permitted transitions and properties, checked separately from port compatibility |
| External effect | An operation outside the Kubernetes API, modelled as an effect with declared outcomes |
| Checkpoint | A test that runs one existing controller through the complete pipeline |
| Receipt | A hash record of every input to a generated artifact or evidence report |
| Evidence kind | `extracted`, `observed`, `declared`, `assumed` or `unresolved` |

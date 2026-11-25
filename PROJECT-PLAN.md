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
| ARCH-1 | Discover reconcilers, setup functions and owned API types from a Go module using type information, with no per-controller Go integration code. | `arch extract` on `examples/refsystem` lists every controller without `+arch:controller` markers. | Met for the supported injection patterns. `sourcegen.Discover` finds the three reference-system reconcilers and their setup functions by type information with no markers, and `arch extract` lists them (#103); the sample database controller's factory-style injection is reported as a located diagnostic. |
| ARCH-2 | Build a resource catalog (GVK, GVR, scope, served versions) from Scheme registrations, CRD manifests and discovery snapshots. Plural names are never guessed. | Catalog for the reference system is loaded from its CRD YAML with no hand-written bindings. | Partial. `KubernetesResourceCatalog` binds expressions by hand. |
| ARCH-3 | Represent cluster, GVK, namespace/name and UID as distinct identity parts; make served-version conversion explicit. | Two objects with the same GVK and name in different clusters are distinct IR objects. | Partial. `ir.ClusterID` exists; `Effect` and `Watch` carry an optional cluster that enters the stable ID only when set, and composition joins never cross clusters without an explicit link (#67, #75). Objects still have no cluster part in their own identity and served-version conversion is not modelled. |
| ARCH-4 | Extract watch registrations: `For`, `Owns`, `Watches`, `WatchesRawSource`, `Named`, `WithOptions`, `WithEventFilter`, standard handlers and predicates including `And`/`Or`/`Not`. Unsupported forms produce a `file:line` diagnostic. | Reference-system setup functions extract with zero diagnostics; a custom `predicate.Funcs` yields one located diagnostic. | Partial. `pkg/watchgen` supports `For`, `Owns`, `Watches` with a package-level map func or `EnqueueRequestForOwner`, and the generation and resourceVersion predicates with `And`/`Or`/`Not`. `pkg/watchmodel` generates event and delivery state from the IR rules (#58 to #64) and the database sample's hand-written watch model is deleted. Map functions that are methods or list objects are unsupported. |
| ARCH-5 | Extract client effects for typed, unstructured and dynamic clients: Get, List, Create, Update, Patch, Delete, DeleteAllOf, Apply, Status and subresources, with options and read consistency. | Every client call in the reference system maps to an IR effect with operation, resource, consistency and options. | Partial. The typed observer records Get, List, Create, Update, Patch, Apply, Delete and DeleteAllOf, the status subresource for Update, Patch and Apply, and the FieldOwner option; the dynamic observer records Get, List, Create, Delete, Patch, Apply and ApplyStatus (#39 to #45). DryRun is refused. Typed status Apply and dynamic DeleteCollection are not recorded; the reference system's client calls are not yet checked end to end. |
| ARCH-6 | Extract reconcile control flow: branches, early returns, error idioms (`IsNotFound`, `IsConflict`), `switch`, bounded iteration, helper calls resolved by symbol, inlined subreconcilers. | Reference-system reconcilers extract with no statement-text mapping callbacks. | Partial. The typed frontend resolves calls by symbol and extracts branches, assignments, returns including requeue results, slice and struct literals, owner-reference helpers, quantities and constant string concatenation, and collects every diagnostic (#103). The reference-system reconcilers extract with zero diagnostics. `switch`, bounded iteration, `IsNotFound` and `IsConflict` idioms, closures and inlined subreconcilers are still unsupported (see testdata/external/cronjob/FOLLOWUPS.md). The sample database controller extracts with the typed frontend as well, its Store calls resolved by symbol and its mutations applied as load overlays (#169). |
| ARCH-7 | Ship a built-in effect summary library for the controller-runtime and client-go client surface. Business summaries are exceptional, pinned to a source hash and listed in the support report. | ConfigMirror's behaviour is generated from the summary library, not hand-written. | Partial. `pkg/summaries` ships Get, Create, Update, Delete, Patch, Apply and List; ConfigMirror's model is generated from it (Checkpoint A, ADR-0007). Not yet listed in the support report. |
| ARCH-8 | One shared, versioned IR with stable IDs, source locations, provenance and evidence kind for every fact. All frontends and generators use it. | No generator reads frontend-specific structs; the IR schema has a version and migration policy. | Missing. Five ad hoc result types exist. |
| ARCH-9 | Model controller-runtime scheduling per pinned version: FIFO and priority queues, cache delivery, deduplication, dirty keys, retries, `RequeueAfter`, startup sync and deletion. | Both queue profiles replay against the native queue; a controller using the default priority queue is modelled without configuration. | Partial. Both queue profiles are modelled after controller-runtime v0.23.3 and replay against the native FIFO and priority queues; startup list-and-sync and deletion tombstone events are opt-in per controller (#55 to #57, #65). The priority queue is the default, as in controller-runtime v0.23; a controller declares `queue: fifo` to model client-go's FIFO queue (#173). `cmd/general-demo` replays both a FIFO profile and a priority profile with startup sync against the native queues and writes a report for each (#175). The native handler sink carries `LowPriority` on a priority-queue controller (#174). A requeue a return node asks for reaches the generated model: the process reports the outcome and the runtime schedules the key after it (#213). |
| ARCH-10 | Compose projects into a system by deriving ports and joins from reads, writes, watches and references, cluster-aware. Ambiguous joins require one explicit binding. | Reference-system projects compose from a `Project` document with no hand-written port list. | Partial. `composition.PortsFromIR` derives ports from effects and watches with evidence and provenance, and `JoinByResource` joins unique providers by resource identity and cluster with ambiguity and unresolved diagnostics (#66, #67). The provider of a resource is its owner (primary watch), or else its sole creator (ADR-0010, #176). `composition.Load` derives the ports of a component that names an IR document and joins them by resource (#177). `arch generate` composes several declared controllers into one model over the shared abstraction state, with the resource bindings derived from their watches and effects (#200), and generates one variable per selected field (#151). The reference system composes: its three controllers and the Flux Kustomization boundary are declared in `project.yaml`, `arch generate` builds one model with five derived bindings and `arch check` samples it (#150, #184). Create, Delete, Status().Update and Status().Patch with client.Merge generate from the built-in summaries, and a `client.ObjectKey` literal resolves through the abstraction. |
| ARCH-11 | Generation is deterministic and incremental; a semantic diff between two IRs reports added, removed and changed controllers, watches, effects and assumptions. | `arch diff` on two commits prints the changed watch. | Partial. Outputs are deterministic; `ir.Diff` and `arch diff` report added, removed and changed records by stable ID (#104). A watch whose mapping changed is one change that names its old ID (#214). Source positions are not content: a moved Reconcile body gives an empty delta (#199). Assumptions are not yet diffed. |
| ARCH-12 | State abstraction is explicit: an `Abstraction` document selects the object fields and locals that enter the model, bounds populations and value domains, declares identity keys and reference fields, and drives both model generation and replay projection. Preserved, excluded and over-approximated behaviours are listed in the report. | ConfigMirror's model state and its replay projection are generated from one abstraction document; the report lists its bounds. | Done for ConfigMirror and the sample database controller (Checkpoint A, ADR-0007, #141): both models and both replay projections are generated from their abstraction documents. Map keys outside annotations and labels are not expressible (#138). |
| ARCH-13 | Operations outside the Kubernetes API are IR effects of kind `external`, identified by callee symbol, with declared outcomes and ordering (evidence `declared` or `assumed`); their internals are not extracted. | A provisioning-style call in a reconciler appears as an external effect with two declared outcomes and is gated during replay. | Partial. `adaptergen.GenerateInterfaceProxy` gates the methods of a Go interface as external effects: a call pauses at the gate until released with a declared outcome, and an undeclared outcome is refused (#119). The extractor does not yet emit `external` effects from callee symbols for arbitrary interfaces, and ordering is not declared. |

### VIS: visualize the architecture

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| VIS-1 | Generate LikeC4 from the IR: system context, component, resource/watch, process, runtime and execution (counterexample) views. | `arch generate` writes one LikeC4 workspace covering all six views for the reference system. | Partial. `pkg/likec4` writes one workspace with one shared specification and the context, resource/watch, process, runtime and execution views (#88 to #93). No component view yet. The example emitters route through `pkg/likec4` and the old `model.LikeC4`, `WatchLikeC4`, `FlowLikeC4` and `dynamicgen.LikeC4` are deleted (#96); `sourcegen.Descriptor.LikeC4` is replaced by `likec4.StructureView`, so one specification remains (#197). |
| VIS-2 | One command renders and serves every view of a project. | `arch render && arch serve` shows all views. | Partial. `arch render` builds the generated workspace into a single-file site and `arch serve` serves it (#95); `make render` and `make serve` run `arch extract`, `generate`, `render` and `serve` on the ConfigMirror sample (#205). |
| VIS-3 | Presentation hints (titles, grouping, layout) are authored in KRM and never add or remove relationships. | A hint referencing an unknown element fails validation; hints change layout only. | Partial. `likec4.LoadHints` reads a `ViewHints` document with titles, groups and layouts; validation fails with `file:line` on an unknown element or view, and a test proves hints leave elements and relations unchanged (#94). `arch generate` applies the document a project names under `viewHints` and records it in the receipt (#206). |
| VIS-4 | Every diagram element and relationship carries its evidence kind and a source link. | Hovering any edge shows `extracted`/`declared`/`assumed` and `file:line`. | Partial. |

### CON: constraints, invariants and contracts

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| CON-1 | Constraints are authored independently as KRM/CEL and never rewritten by extraction. | Mutating source changes the model, not the constraint files. | Met. |
| CON-2 | Safety invariants are expressed over composed system state and events using stable identities, not process node indices. | A constraint referencing an effect survives a source edit that renumbers nodes. | Partial. Events are `node<N>_<kind>`. |
| CON-3 | Structural contracts (`InteractionContract`) are checked against Go types in owned or upstream modules via markers or external selectors. | An upstream type from the module cache is checked without annotation. | Met. |
| CON-4 | Port compatibility and binding: ports carry a contract name, version, type and direction; unique providers join automatically; mismatches and ambiguity fail. This establishes connectivity only. | Two providers of one contract fail until bound explicitly. | Met. |
| CON-5 | The CEL subset is documented and extended (`exists`, `has`, lists, nested records) only with a sound Quint translation and a differential check. | Each new construct has a CEL-versus-Quint differential test. | Partial. `exists` and `all` quantify over domains declared on the model spec, with a CEL-versus-Quint differential test and located compile errors (#111). Generated models declare one domain per resource with the IDs of its declared objects, so a constraint quantifies over a population (#212). `has`, nested records and quantifiers over map keys are not supported. |
| CON-6 | Temporal properties (eventually, leads-to) are supported with explicit fairness assumptions, or the report states they are unchecked. | A liveness property either runs under `quint verify` with stated fairness or is reported as unchecked. | Partial. `ModelConstraints` declares `eventually` and `leadsTo` properties with weak or strong fairness per document; `arch check --verify` runs `quint verify` with the TLC backend under a time and heap cap and reports each property as verified, violated with a counterexample, or unchecked with a reason (#112). Without `--verify`, fairness or Java the property is unchecked, never passed. CI has no Java, so the TLC tests skip there. |
| CON-7 | Counterexamples are minimized, source-linked and replayed against Go. | A violation report lists the shortest trace found, each step's source location and the replay result. | Partial. Sampling can be steered towards process completion with per-action weights that keep every action reachable, and the receipt records the strategy (#140): on ConfigMirror the successful write is reached in 37 of 40 traces instead of 3. The committed ConfigMirror evidence is sampled with that strategy since #190: the write is reached in 37 of 40 traces, and the receipt and report record the weights. `arch check` keeps the violating trace under `counterexamples/` with an execution view whose steps link to the process source (#203), and `--verify` keeps the TLC counterexample of a violated temporal property the same way (#210). Counterexamples are not yet minimized. |
| CON-8 | The mutation suite is data: every constraint has at least one mutation it detects. | `arch check --mutations` reports a detection matrix. | Done. Both examples express their mutations as `MutationSuite` documents run by `pkg/mutation` (#85); every database constraint has a detected mutation (#188). `arch check --mutations` runs a suite with the model detector, which regenerates and samples the mutant, and the replay detector, which rebuilds the generated adapter against the mutant, reports the matrix and fails on an escaped mutation (#202). |
| CON-9 | Every generated model is checked by evaluating the original CEL against Quint traces. | Differential check runs for any `TransitionModel`, not one example. | Partial. |
| CON-10 | Behavioural protocol contracts: a versioned `ProtocolContract` declares the roles on a binding, preconditions, permitted transitions and independently authored properties; conformance to it is reported separately from binding. Auto-linking is never evidence of protocol conformance. | A bound link whose consumer transitions out of order violates its protocol property. | Partial. `pkg/protocol` loads a versioned `ProtocolContract` with roles, states, transitions, guards and properties; `composition.ComposeWithProtocols` adds a per-link monitor and `_conforms` property, reported in `Result.Protocols` apart from bindings (#116, #117). Only writes move the monitor; reads are not protocol steps. The report lists bindings and protocol conformance in separate sections, and a bound link without a protocol reads "conformance not checked" (#118). `arch check` and `arch replay` list the bindings of a composed model from the composition document `arch generate` writes (#207); `arch` has no protocol links yet, so its protocol section stays empty. |

### ADOPT: adoption by any controller-runtime project

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| ADOPT-1 | The supported subset needs zero per-project Go integration code. Inputs are markers, one `Project` document, a catalog source and constraints. | The reference system has no `example.go`, `adapter.go` or `watches.go` equivalents. | Partial. ConfigMirror's remaining Go glue is fixture construction, client wiring and a no-op for internal process steps; its worker routing is generated (#139). Its extraction options and runtime settings come from `examples/configmirror/project.yaml`, the same `Project` document `arch generate` reads (#204); `generate.go` derives them from the controller declaration. `arch generate` writes the replay adapter of a one-controller model under `adapter/`, with the program that builds the fixtures and wires the reconciler, and `arch replay` builds it in the module with a build overlay and runs it (#201); ConfigMirror replays through it with no hand-written glue. The reference system has no adapter code yet. The external CronJob controller needed no Go code and no marker: only a `Project` document with a hand-written catalog, an abstraction and a controller declaration (ADR-0008). |
| ADOPT-2 | The marker set is minimal; kubebuilder markers are reused; markers are needed only for ambiguity. | `+kubebuilder:object:root=true` types are resources without `+arch:resource`. | Partial. |
| ADOPT-3 | Replay adapters (client instrumentation, key routing, projection, boundary gating) are generated from the IR, catalog and abstraction document. Generated adapters read implementation state only; they never derive observations from expected model state. | `examples/database` runs with its handwritten adapter deleted; a projection test fails if it reads model state. | Partial. Projection, environment, process gating, worker-event routing, reference checks and interface gating proxies are generated with import-restriction tests (#137, #139, #119). The database sample's handwritten adapter is replaced except for fixtures and client wiring (#79); its native watch delivery (the informer view of each watched object, the native predicates and handlers, the typed or unstructured objects they receive and the watch model's variables) is generated, with the extracted rules and the typed constructors supplied at run time (#191), and its invocation driving (the reconcile and idle events, the outcome, the invocation and create counters and the locals assigned from effect results) is generated (#192). |
| ADOPT-4 | A support report lists included, summarized and unsupported constructs with locations. | `arch support` output for the reference system is empty of unsupported items. | Partial. `arch support` prints included, summarized and unsupported items with locations, and `--strict` fails on them (#105). The built-in summaries are not yet listed. On the external CronJob controller the report lists every unsupported construct of `SetupWithManager` and `Reconcile` in one run, 29 items at the pin, and summarizes the setup preamble statements it recognises (ADR-0008, #143, #154). |
| ADOPT-5 | One CLI: `init`, `extract`, `generate`, `render`, `serve`, `check`, `replay`, `diff`, `support`. | `arch` imports nothing under `examples/`. | Partial. All nine subcommands exist and `cmd/arch` imports nothing under `examples/` (#98, #99, #104 to #107, #95). `generate` handles one controller per project; `replay` has no generated adapter to drive and `--mutations` only validates the suite. |
| ADOPT-6 | The library is embeddable and published under a stable module path with a compatibility policy. | `go get github.com/phoban01/generative-architecture@v0.1.0` works. | Done. The module path is `github.com/phoban01/generative-architecture` (#109). v0.1.0 tags 78b4f0e, which passed CI. [docs/release.md](docs/release.md) holds the checklist and the compatibility policy for `pkg/` (#110). |
| ADOPT-7 | External tools are version ranges, not exact pins. | Quint 0.33.x and later patch releases are accepted. | Partial. Quint is accepted in [0.33.0, 0.34.0) and LikeC4 in [1.59.0, 1.60.0) by `pkg/quint` and `pkg/likec4` (#95). |
| ADOPT-8 | Two execution environments: the fast fake harness and envtest. Local runs may skip envtest when assets are absent; the CI integration job installs assets and fails if it cannot run them. | The envtest job runs in CI and fails on missing assets; local tests skip. | Partial. `pkg/envtest.Start` runs envtest when KUBEBUILDER_ASSETS is set and skips otherwise; `make envtest` installs the assets with setup-envtest and fails on a skip, and the CI `envtest` job runs it on both supported Go versions (#80, #81). Only the reference system has an envtest test so far. |
| ADOPT-9 | A neutral multi-controller reference system is exercised end to end by the CLI; domain examples become regression fixtures. | `examples/refsystem` is the walkthrough in README. | Partial. The reference system is the README walkthrough and the example of `docs/adopt-owned-project.md` (#110). `arch extract` finds its three controllers and seven watches with no unsupported construct; with no abstraction, 39 fields are unresolved and `--strict` fails (#103). `arch generate` takes one controller per project, so `arch` does not model or compose it, and the owned-project guide uses ConfigMirror for the later steps. The database sample and ConfigMirror are regression fixtures. `docs/adopt-upstream-project.md` walks the external CronJob controller. Tests run every command of both guides (#134, #110). |

### EXT: external and upstream projects

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| EXT-1 | A `BoundaryModel` document describes an unmodified upstream project: pinned version, contracts, assumptions with evidence kind, and a conformance hook. | A boundary model with an `assumed` fact appears in diagrams as assumed. | Partial. `pkg/boundary` loads a `BoundaryModel` with a pinned version, owned and watched resources, ports, contracts and assumptions whose evidence must be `declared` or `assumed`, and each assumption names a conformance hook; `composition.Load` accepts boundaries as `assumed` components (#68, #69). Boundary evidence is not yet drawn in the LikeC4 context view. |
| EXT-2 | Source in dependency modules can be selected from the module cache without annotations. | A selector for a type in `k8s.io/api` resolves and hashes its source. | Met. An external selector resolves `k8s.io/api` types from the module cache with `GOPROXY=off`, hashes them as `module@version/path` and fails clearly when the module is absent (#72). |
| EXT-3 | Cluster API: provider contracts ship as `InteractionContract`s; infrastructure and bootstrap providers extract through the standard frontends; the multi-cluster cache is a boundary. | The InfrastructureMachine contract checks a provider type from the module cache. | Partial. The multi-cluster cache is a boundary with a management cluster and two workload clusters, pinned to cluster-api v1.9.0, and cross-cluster bindings need an explicit link (#75). InfrastructureMachine (v1beta1 and v1beta2), InfrastructureCluster and BootstrapConfig (v1beta1 and v1beta2, #187) ship as `InteractionContract`s under `pkg/contracts/library/clusterapi` and check a provider fixture from the module cache (#70 to #72). Optional fields can be declared `together`, so an endpoint with a host and no port fails (#186). Contract fields may be lists, maps of primitives and `conditions` (a `metav1.Condition` list), and the three contracts check `status.conditions` (#185); `status.failureDomains`, a map of structs, is still not checkable. |
| EXT-4 | Flux: source, kustomize and helm controllers have boundary models; server-side apply and patch effects, `interval` and `dependsOn` are modelled. | A system containing a Flux `Kustomization` composes with an owned controller that watches its outputs. | Partial. Kustomization, GitRepository and HelmRelease boundary models ship under `pkg/boundary/library/flux`, pinned to their controller versions, with `interval` as a bounded requeue, `dependsOn` as an apply guard and server-side apply as a declared assumption (#73, #74). Applied outputs use an authored contract rather than a resource, so they do not yet join by resource. |
| EXT-5 | No implicit fetching; every input is pinned and receipted. | Receipts list every file and module hash used. | Met. |

### EVID: evidence and CI

| ID | Requirement | Acceptance test | Status |
| --- | --- | --- | --- |
| EVID-1 | Evidence receipts are immutable and invalidated when any input changes. | Editing a watched file fails replay until regenerated. | Partial. Receipts hash the full in-module closure. Drift in an owned input (the selected packages and the extra documents) fails replay; drift in a library input warns and is recorded (#142, ADR-0009). |
| EVID-2 | Reports distinguish safety violations, conformance mismatches, unsupported constructs and tool failures. | Each failure has a category field. | Partial. `report.Verification` categorises findings as safety, conformance, unsupported or tool and embeds the receipt (#107); it records the mode and per-category counts, and the CLI and all three example runs write it (#83). |
| EVID-3 | Inventory mode reports unresolved facts; verification mode refuses incomplete semantics for the selected scope. | `arch check --strict` fails on an `unresolved` fact in scope. | Partial. `--strict` fails on unsupported or unresolved items in the selected packages; inventory mode reports them (#105). Receipts split the closure: owned drift fails and library drift warns (#142, ADR-0009). |
| EVID-4 | A CI template runs the pipeline and publishes diagrams, receipts and the diff. | The template runs green on the reference system. | Partial. The reusable workflow `.github/workflows/arch.yml` runs extract, generate, check, replay, render and diff and uploads diagrams, receipts, traces and the IR diff; the repository's `architecture` job runs it on ConfigMirror and `make arch-pipeline` reproduces it locally (#108). Replay runs non-strict until `arch` has a replay adapter, and adopters must build `arch` from source until the module is published (#109). |
| EVID-5 | Receipts cover the consumed input closure (transitive source of selected packages, schemas, configuration, abstraction, boundary assumptions) and record the exact resolved tool versions. Compatibility (version ranges) and reproducibility (exact versions in receipts) are separate concerns. | A receipt lists every file consumed and the exact Quint and LikeC4 versions; changing a transitive dependency file invalidates it. | Partial. Receipts list the Go files, embedded files and go.mod of the selected packages and their in-module imports, the extra documents and the exact Quint and Go versions (#142). A changed library file warns rather than invalidates (ADR-0009). The LikeC4 version is not recorded, and out-of-module dependencies are covered by go.sum only. |

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
| **B: external adoption** | After Checkpoint A and the M2 builder support needed by the chosen controller | A controller that was not written for the extractor is pinned, extracted, modelled and either replayed or reported as unsupported with located diagnostics | A pinned upstream controller under `testdata/external/` has a committed support report and generated model; the adoption guide was validated against it. Status: reported as unsupported. The CronJob tutorial controller has a committed support report, abstraction and `incomplete` VerificationReport, and the draft guide was walked on it. No model is generated, because extraction fails closed at the setup preamble (ADR-0008). |
| **C: release** | End of M7 | The whole surface on the reference system | Tag v0.1.0. Status: tagged at 78b4f0e on 2026-10-28 after CI passed. [docs/release.md](docs/release.md) lists the steps and their state. The reference system extracts but is not modelled, because `arch generate` takes one controller. |

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
- [ADR-0008](docs/adr/0008-checkpoint-b-findings.md): Checkpoint B findings: what adopting an unmodified kubebuilder controller required, ranked extractor gaps and the open decisions.
- [ADR-0009](docs/adr/0009-receipt-drift-scope.md): Receipts split the input closure into owned inputs, whose drift fails, and library inputs, whose drift warns.

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

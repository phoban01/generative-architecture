# Project plan: generated architecture and behavioural verification

## Purpose

Build a reusable Go library and CLI that turns a collection of existing controller repositories into a maintained system architecture and an executable behavioural model. Engineers should express constraints against that generated model, explore it with Quint, and replay useful executions against the actual controllers.

The architecture must follow implementation changes. A second, manually maintained description of each controller's flow would defeat the purpose. Human input remains appropriate for system selection, domain bounds, requirements, unresolved business semantics and abstractions of external systems.

This plan focuses on work, dependencies, deliverables and priorities.

## What “over the line” means

The first usable release should support a real end-to-end slice of the machine-vendor system from a central architecture repository:

1. Select pinned versions of MAC, the claim/lease controller and a CAPI infrastructure provider.
2. Discover their resources, selected processes, client operations and watch registrations.
3. Generate the connections between those processes, including object-key mappings.
4. Compose them with a versioned controller-runtime model and explicit upstream abstractions.
5. Generate Quint and LikeC4 from the same intermediate representation.
6. Evaluate independently authored constraints and replay counterexamples or sampled executions against Go.
7. Regenerate on source changes and present architecture changes, unsupported constructs and verification evidence in CI.

The pilot should work with the existing organisation of the production code. Subsystems may span packages and controllers; adopting the tool must not require an architectural refactor.

A first release will cover a documented Go/controller-runtime subset. Supporting any repository without selection, bounds or semantic summaries is a longer-term aspiration, not a credible first-release promise.

## System assumptions to retain

MAC watches InfraMachines, determines whether a provider satisfies its machine-assignment contract, creates/reuses a MachineClaim, waits for fulfillment, then publishes the assigned machine ID to InfraMachine status. MachineClaim events map back to the associated InfraMachine's reconcile key.

The claim/lease subsystem assigns physical machines. The infrastructure controller receives a machine ID and makes the provisioning call; it does not interpret lease semantics. MAC clears or changes the assignment when a lease is revoked. There is currently no independent provisioning-time lease check. The model must retain that fact and explore the resulting races, rather than assume an unimplemented fencing guarantee.

The reserve pool is a dedicated Kubernetes cluster. The wider system includes reservations, priority preemption, replacement after failure and diagnostics/repair. These are later domain slices, built on the same generation pipeline.

## Current position

| Area | Present in the prototype | Main gap |
| --- | --- | --- |
| Model core | Typed state/actions, CEL evaluation, Quint generation, trace replay | One shared representation for discovery, behaviour, runtime and diagrams |
| Go extraction | Restricted branches, assignments, returns and explicit subreconciler composition | More robust symbol/data-flow analysis and less example-specific mapping |
| Contracts | Structural declarations on owned types; selectors for upstream types | Joining behavioural interactions automatically across real projects |
| Watches | Selected `For`/`Watches`, predicates, reference-field mapper, explicit unstructured GVKs | General runtime registration discovery, additional mapper patterns and composition |
| Dynamic clients | Selected GVR-based access extraction; unstructured objects; observed Get/Create/Update/UpdateStatus | Discovery/CRD ingestion, additional operations and general behaviour lowering |
| Runtime | Real workers/FIFO queues, multiple controllers/keys/workers, controlled retries and cache fixtures | A fully integrated generated model of real controller setup and informer behaviour |
| Composition | Versioned ports, shared-state bindings, unique-provider automatic linking | Deriving those ports and joins from the discovered implementation |
| Conformance | Saved/fresh traces, independent observation, source and adapter mutations | More generated adapters and API-server-backed evidence |
| Packaging | Public Go packages, separate-module consumer, KRM documents and examples | Stable APIs/module path, one CLI workflow and CI integration |

The dynamic-client slice has been integrated into the walkthrough and generated architecture. Its baseline checks include the full repository verification, targeted race tests and 40 fresh Quint traces replayed against Go. These are bounded conformance results, not an exhaustive system proof.

The assignment example still contains handwritten operation summaries, finite resource bindings, event/queue composition and fixture projection. Dynamic access extraction currently produces dependency facts and checks their association with declared effects; it does not derive every branch inside the API helper. These are the principal seams to generalize, not proof that the entire system is already generated.

## Intended architecture

The central architecture repository selects inputs and owns requirements. The library interprets implementation facts and composes models. Domain repositories retain their existing code layout.

```text
Pinned repositories, Go modules, CRDs, deployment configuration
                          |
              Source and resource discovery
                          |
       Typed architecture/behaviour representation
                /                         \
     Versioned semantic summaries      Independent constraints
                \                         /
                  Composed system model
                    /              \
             LikeC4 views        Quint exploration
                                      |
                            Execution / counterexample
                                      |
                            Actual-controller replay
                                      |
                         Evidence, source links and CI diff
```

The representation should distinguish facts extracted from source, facts observed in an execution, declared summaries/assumptions, and unresolved behaviour. A successful sample run must not turn a declared assumption into an extracted fact.

## Phase 0 — Consolidate the prototype

Make the existing work a clear starting point before extending the extraction surface.

- Establish the MAC assignment walkthrough as the main example. Keep the larger publication/runtime examples as focused references and regression fixtures.
- Retain the newly integrated dynamic-client example, generated artifacts and verification evidence as part of the baseline. Keep the supported API operations explicit.
- Consolidate generation, diagram rendering, fresh exploration and saved-trace replay into one CLI workflow. Today `walkthrough`, `arch`, `make generate` and the rendered site cover different slices.
- Produce one coverage report showing which source selections and runtime behaviours are included, summarized or unsupported.
- Preserve a baseline of meaningful mutations: missing watch, wrong key, filtered status update, wrong GVR/GVK, wrong publication ID and stale assignment.
- Record the current boundaries: fake APIs, controlled informer delivery, finite object domains and sampled exploration.

**Deliverables:** coherent walkthrough and commands, current evidence, support matrix, baseline mutation suite and a short record of architectural decisions.

## Phase 1 — Establish the shared representation and resource catalog

This is the foundation for automatic composition. Extend the existing model types rather than creating another unrelated model format.

### Resource and object identity

Represent cluster identity, API group/version/kind, group/version/resource, namespace scope and object identity separately. Names alone are insufficient across clusters or object deletion/recreation. Include UID and resource-version abstractions where those affect behaviour.

Management-cluster CRs, customer-cluster Nodes and reserve-cluster Nodes must remain distinct even when they share a GVK or a name. Model API version conversion explicitly when joining different served versions.

### Resource catalog

Combine information from Go Scheme registrations, CRD schemas and captured API discovery. Use explicit GVR/GVK mappings and resource scope; do not derive plural resource names by guessing from a kind.

For dynamic clients, resolve resource expressions to a finite set of catalog entries. Retain the selection expression and its provenance. Discovery-dependent choices that cannot be resolved statically should become bounded model inputs or remain visibly unresolved.

### Common records

Define reusable records for controllers, processes, effects, watches, predicates, event-to-key mappings, object references, contract implementations, runtime profiles and constraints. Give them stable IDs and source locations.

Store effect semantics separately from source identity. A Get call establishes a read dependency; its cache consistency, possible outcomes and state projection require additional semantics.

### Provenance

Track repository revisions, module versions, selected source hashes, CRD/discovery snapshots, configuration, summary versions, generator versions and model bounds. Propagate these into generated diagrams and evidence receipts.

**Deliverables:** shared versioned representation, catalog loaders, provenance records and schema migration policy.

**Dependency:** Phase 0. Later phases should consume this representation rather than inventing new joins.

## Phase 2 — Expand source discovery and behavioural extraction

Replace fragile example-specific syntax matching with reusable analysis, while retaining a clearly bounded language subset.

### Controller and process discovery

Use `go/packages` and type information to identify manager setup, reconciler implementations, resource schemas, dependencies and selected subreconcilers. Resolve imported aliases, helper functions and constructor-injected clients by symbol identity.

Use small annotations or external selectors for ambiguous entrypoints and contract declarations. Do not require annotations that repeat every API call, watch or branch already visible in Go.

### Behaviour analysis

Use typed control-flow and, where useful, SSA/data-flow analysis to track conditions, return values, assignments, error propagation, object snapshots and object aliases across supported helper calls.

Make subreconcilers first-class processes. Preserve their inputs, retained snapshots, effects, errors and continuation policy when composed into a parent controller. API calls inside a helper must remain observable interleaving points.

Handle ordinary controller patterns incrementally: early returns, not-found handling, explicit conflict retries, finite iteration over selected resources and status updates. Unsupported reflection, unrestricted loops, indirect calls and concurrency must produce a precise diagnostic or require a reviewed summary.

### Client effects

Use a common effect representation for typed controller-runtime clients, unstructured controller-runtime clients and `dynamic.Interface`. Extend beyond the current operations according to the pilot's actual usage: List/selectors, Patch, Delete/finalizers, Apply and relevant subresources.

Include request options, patch contents, field ownership and status/spec distinctions in semantics where needed. Missing fields, wrong JSON types and schema changes are particularly important for unstructured access.

Reuse summaries for Kubernetes/library primitives. Business helper summaries should be exceptional, source-bound and visible in the coverage report. Add an independent check when a summary is claimed to match a real helper.

**Deliverables:** reusable source frontends, subreconciler call graph, operation summaries, source-linked diagnostics and a versioned support matrix.

**Dependency:** shared identities and representation from Phase 1. Start with constructs present in the real MAC slice rather than broad language coverage.

## Phase 3 — Generate watch and runtime connections

Unify watch discovery with the existing scheduler model so a resource write leads to the appropriate future reconciliation without hand-authored queue transitions for each example.

### Registration and key routing

Support the pilot's `For`, `Owns`, `Watches` and selected source registrations. Extract per-watch/global predicates, owner-reference semantics, explicit reference fields, indexed lookups and bounded fan-out to reconcile keys.

Include old and new objects for update mappings. References can change, causing both old and new target keys to need reconciliation. Preserve namespace, cluster and UID distinctions where relevant.

For runtime-selected resource types, instantiate watch rules from the bounded resource catalog. Analyze supported registration loops and conditional setup, retaining their conditions. A dynamic client alone does not imply that runtime watch registration has been discovered.

Add optional instrumented manager/setup execution to capture registrations and compare them with static extraction. Runtime capture supplies evidence for the configurations executed; it cannot establish that all possible registrations were observed.

### Runtime semantics

Version runtime profiles against controller-runtime/client-go. Compose cache snapshots and delivery, pending events, predicate evaluation, queue deduplication, dirty/processing keys, worker invocation, errors, requeue delays and rate limiting.

Add startup/cache-sync, deletion/tombstone and restart/relist behaviour in the order required by the pilot. Do not force the existing FIFO model onto deployments using a different queue policy.

Keep environment assumptions explicit: delivery fairness, scheduler fairness, watch interruptions and retry limits affect eventual-progress claims. Safety checking can proceed with a weaker set of assumptions than liveness checking.

**Deliverables:** generated event-to-key graph, reusable runtime composition, registration comparison and profiles for the pinned runtime versions.

**Dependencies:** Phases 1 and 2; existing queue/worker work is reusable.

## Phase 4 — Compose projects and contracts automatically

Make the central architecture repository assemble the model from implementation-derived identities and interactions.

- Pin project revisions, Go module replacements/build tags, CRDs, deployment configuration and selected controllers in KRM input documents.
- Generate resource ports and data dependencies from discovered reads, writes, watches and reference mappings.
- Match contract implementations using structural declarations or external schemas/selectors. Retain the actual code's capability/discovery checks as behaviour.
- Join compatible ports using resource identity, cluster context, object-key relations, contract versions and configuration. Two components mentioning the same kind is only a candidate interaction.
- Automatically connect unambiguous relationships. Surface ambiguous providers and require a small explicit binding for those cases.
- Preserve private snapshots, resource ownership and effect ordering. Avoid accidentally aliasing a cached observation to current API state.
- Describe external systems through versioned boundary models and independently recorded assumptions. Do not silently substitute desired behaviour for unavailable implementation.

For upstream CAPI, begin with the chosen infrastructure provider and the assignment/provisioning boundary. Prefer source extraction where feasible and observable boundary conformance elsewhere. Use a controlled test checkout or injectable public dependencies when upstream internals are inaccessible; avoid requiring production forks solely for architecture annotations.

For cert-manager, initially include the relevant resources and controller boundary. Extract or summarize certificate behaviour only if it affects a property being investigated. The architecture inventory can include a component before its complete behavioural model exists.

**Deliverables:** multi-project system loader, generated ports/bindings, resolution diagnostics and pinned upstream boundary packages.

**Dependencies:** resource catalog, source effects and watch/runtime connections. This phase removes the central example-specific composition code.

## Phase 5 — Strengthen model-based testing against actual controllers

Move from useful fake-based conformance to a repeatable testing workflow for real implementations.

### Reusable adapter generation

Generate supported client instrumentation, key routing and projection scaffolding from the common representation and resource schemas. Supply domain-specific observations only where they cannot be derived reliably.

Keep the implementation independent of the expected trace state. The scheduler controls permitted interleavings and environment changes; it must not prescribe branch outcomes or fill API objects with the model's expected result.

Capture API requests/responses, snapshots, return values and queue behaviour. Make the distinction between control-flow steps and observable effects explicit, including allowed stuttering between observations.

### Two execution environments

Keep the fast controlled worker/fake-API harness for schedule exploration and mutation tests. Add envtest coverage for real CRD validation, status subresources, resource versions, conflicts, finalizers and informer behaviour relevant to the pilot.

Use a fuller cluster test only for behaviours envtest does not provide, such as external controllers or cluster lifecycle interactions. Treat each environment's limits as part of its evidence.

### Exploration and failure reporting

Provide deterministic regressions, seeded Quint sampling and bounded exhaustive/model-checking workflows where tractable. Control state growth through subsystem slicing, finite identities, domain abstraction and process composition.

Minimize counterexamples and link each step to source and an observed effect. Preserve the exact source/runtime/profile configuration needed to reproduce a failure. Revalidate minimized executions against Go.

Maintain deliberate mutations across extraction, composition and implementation so a passing test is evidence that relevant disagreements can be found. Keep safety violations, conformance mismatches, unsupported behaviour and tool failures distinct in reports.

**Deliverables:** reusable adapter generation, envtest harness, reproducible counterexamples and layered evidence reports.

**Dependencies:** Phases 2–4. API-backed probes can begin earlier to inform the runtime summaries.

## Phase 6 — Integrate the real machine-vendor pilot

Use a narrow real slice to challenge the abstractions before expanding the system model.

### First slice: assignment and provisioning

Map the actual MAC setup/subreconcilers, machine-assignment contract discovery, claim/lease fulfillment, provider status publication and provisioning call. Add the relevant customer and reserve-cluster context.

Replace the miniature's `fulfillClaim` environment action with the real claim subsystem process. Replace fixture contract annotations with the actual discovery mechanism. Retain temporary summaries only with explicit provenance and scope.

Investigate constraints such as incompatible resources never receiving assignments, at most one effective claim per consumer, publication matching the observed fulfilled claim, correct key routing, and no incompatible simultaneous ownership of a physical machine. Define retry, stale-cache and deletion scenarios alongside normal allocation.

Treat “provisioning cannot proceed after lease revocation” as a question to investigate under the current design. If the model exposes a race, record it and the relevant assumptions. Changing the production protocol is a separate decision; the architecture work does not require a refactor to conceal or fix every finding.

### Subsequent slices

Add priority preemption and release/handoff, failure replacement, diagnostics/repair and return to reserve, then reservation/capacity guarantees. Each slice should reuse the established primitives and compose with the assignment model.

Compare the discovered architecture with the team's understanding to find missing relationships, implicit contracts and scattered responsibilities. Preserve those findings as source-linked architecture information.

**Deliverables:** a useful central architecture repository for the real system, documented findings and a prioritized queue of additional domain slices.

**Dependencies:** Phases 1–5. Reading the real repositories should start early; the full pilot integration comes after the shared pipeline is usable.

## Phase 7 — Make generation part of normal development

Package the prototype into a tool teams can use without maintaining a custom demo runner.

- Choose and publish the Go module path; document the extension interfaces and compatibility policy.
- Provide one CLI with project selection, extraction, composition, diagram generation, checking and replay. Label proposed command names before they are implemented.
- Add a small library API for embedding generation in existing tooling; avoid requiring an npm application. Quint and LikeC4 can remain external pinned tools.
- Generate LikeC4 context/component, process, resource/watch and execution views from the common representation. Allow stable presentation hints without duplicating behavioural connections manually.
- Run incremental generation on source/configuration changes. Produce a semantic architecture diff with source links and distinguish changes to implementation, assumptions and constraints.
- Publish immutable evidence and diagrams as CI artifacts. Invalidate evidence when relevant code, runtime versions, schemas, configuration, summaries or bounds change.
- Keep an inventory mode that reports unresolved parts and a verification mode that refuses incomplete semantics for the selected verification scope.
- Document adoption for both owned repositories and unmodified upstream dependencies, using the machine-vendor pilot plus a second unrelated controller as examples.

**Deliverables:** versioned library/CLI release, CI template, generated architecture review and concise adoption documentation.

**Dependencies:** the pilot should inform the public interfaces; publishing too early would freeze the current example-specific seams.

## Sequence and workstreams

| Workstream | Start with | Then |
| --- | --- | --- |
| Core representation | Phase 0 consolidation and Phase 1 identities/catalog | Composition, provenance and architecture diffs |
| Source analysis | Real-repository inventory and selected Go processes | Typed/dynamic effects, watch mappings and subreconcilers |
| Runtime and conformance | Existing queue harness plus targeted API-server probes | Generated runtime composition, adapters and replay |
| System integration | MAC/claim/provider dependency map | Real assignment slice, upstream boundaries and domain expansion |

The main dependency chain is **shared identities → extracted effects/watches → generated joins → actual-controller conformance → pilot-informed release**. Workstreams can overlap, but expanding the language frontend without a real-system inventory risks supporting the wrong patterns.

Roles are responsibilities, not staffing assumptions. One person may cover several. Scheduling estimates should follow the repository inventory and the first API-backed integration spike; no calendar commitment is implied by this plan.

## Immediate next work

1. Inventory the actual MAC, claim/lease and provider repositories: setup patterns, subreconcilers, client forms, schemas, configuration and upstream versions.
2. Write the shared resource/object identity and provenance design, including management/customer/reserve cluster boundaries.
3. Import CRD/discovery data into the resource catalog so GVR/GVK bindings stop being manually repeated for each provider.
4. Move the miniature's handwritten watch/queue composition into reusable library code driven by the common representation.
5. Add one real API-server-backed assignment experiment to expose which existing summaries need adjustment.
6. Use the findings to choose the first source-analysis extensions and consolidate the CLI entrypoints.

## Main risks and how the plan addresses them

| Risk | Approach |
| --- | --- |
| The generated model merely repeats its own assumptions | Separate requirements and observations; track summary provenance; use implementation/source mutations and API-backed checks |
| Source extraction silently misses behaviour | Typed analysis, precise unsupported diagnostics, scoped coverage reports and strict verification mode |
| Dynamic resource discovery makes the model unbounded | CRD/discovery catalogs, explicit finite selections and visible unresolved resource choices |
| Models become too large to explore usefully | Slice by property and subsystem; bound identities; reuse abstractions and compose incrementally |
| Runtime summaries drift from dependencies | Version profiles and run conformance against the pinned controller-runtime/client-go versions |
| Upstream internals resist instrumentation | Observable boundary models, injected public dependencies and controlled test checkouts |
| The architecture becomes another manual specification | Generate relationships and behaviour from code; reserve handwritten input for requirements, bounds and unresolved semantics |
| Teams cannot tell which example or artifact is current | One workflow, source-linked artifacts, immutable receipts and a single maintained walkthrough |

# From the prototype to the existing system

The implementation need not be refactored into the boxes in the diagram. A component contract can describe behavior scattered across several packages, reconcilers or repositories. The architecture repository records those mappings.

The machine assignment boundary supplied by the system owner is:

1. A CAPI infrastructure provider implements the MAC-facing InfraMachine contract.
2. MAC recognizes a compatible InfraMachine and creates a MachineClaim for it.
3. That claim obtains a machine lease through the existing claim/lease mechanism.
4. MAC observes the claim's machine assignment and reports the machine ID to the InfraMachine.
5. The infrastructure controller uses that machine ID in its provisioning call. It does not interpret leases.

The concrete contract fields, interface methods and claim controller placement have not been supplied. The stated revocation mechanism is MAC clearing/changing the InfraMachine assignment, with no independent authorization check at provisioning. Any additional synchronization or cancellation guarantees remain unspecified. The prototype must label its choices as abstractions rather than discovered facts. In particular, observing an assignment and making a provisioning call are separate actions: a lease can change between them.

## Implemented source extractor

The demo now uses controller-tools' marker parser and Go type information to extract API GVKs/field paths and controller/action symbols. A resource needs `+arch:resource`; group comes from the package's `+groupName`, version from the package name (or `+versionName`), and kind from the type name. The controller has `+arch:controller=mac` and action methods carry tags such as `+arch:action=publish`.

`source-mappings.yaml` supplies primary/owned resource declarations and four field mappings. Existing contracts supply protocol names, directions and identity keys. `arch generate` extracts and checks these mappings before producing Quint, LikeC4 and Go dispatch. See `SOURCE-CODEGEN.md` for the example and drift failures. Stock controller-gen separately generates API deepcopy code.

The `watchgen` frontend now extracts a bounded set of `For`/`Watches` builder registrations, predicates and local reference-field mappers; `examples/assignment` connects those rules to real handlers and the native queue harness. Dynamic registration discovery, arbitrary Go-to-model type mapping and automatic selection of upstream source remain future work. Behavioral rules and safety properties stay authored and reviewed; extracted implementation changes do not silently redefine expected behavior.

## Joining the real repositories

The central repository should pin repository commits or release artifacts and contract hashes. For each port, retain the source location and evidence kind: extracted, declared, assumed, or tested. A project implements the shared MAC contract; the system manifest selects the deployed provider instances.

The semantic joins are:

- InfraMachine identity: management cluster ID + object UID, with namespace/name as lookup data rather than durable identity.
- MachineClaim ownership: an explicit reference to that InfraMachine identity.
- Claim assignment: a reference to the leased physical machine identity.
- MAC publication: the concrete field or method in the InfraMachine contract that carries the machine ID.
- Provisioning: the infrastructure controller's call to the machine service using that ID and its caller identity.

Names, RBAC permissions and shared CRDs are useful discovery hints. They do not establish these behavioral joins on their own. Once the semantic adapters exist, new instances can be connected automatically using their typed references; incompatible or ambiguous joins must fail compilation.

## Executing against controller-runtime

The demo now includes generated Go action wiring and a handwritten adapter for an illustrative MAC reconciler using controller-runtime's fake client. See `CONTROLLER-DEMO.md`. Model traces execute the reconciler's phase methods; a separate test exercises its complete `Reconcile`. The other actors remain environment fakes.

The [runtime conformance layer](RUNTIME-CONFORMANCE.md) adds a versioned KRM RuntimeProfile, real worker/queue replay, controlled cache observations and native informer/delaying-queue tests. The [process extractor](BEHAVIOR-EXTRACTION.md) separately analyzes publication control flow. The [composition layer](COMPOSITION.md) now joins the extracted publication nodes with runtime events and replays the unmodified method through paused client calls. Parent ordering and upstream adapters remain explicit integration work.

For production conformance, replace the demo implementation with an adapter for the real controllers. It should create/update the actual CRs, run the actual reconcilers under envtest, control the fake machine service, and project observable state to the model's state. Retain the test driver's handwritten mappings independently from the authored Quint transitions.

A useful first real slice is MAC + one InfraMachine implementation + the claim controller. Keep CAPI's remaining behavior as a versioned environment contract initially. Test that contract against the pinned upstream release before calling it verified. cert-manager can be added the same way when certificate lifecycle behavior becomes relevant.

Do not assume one Reconcile invocation equals one model transition. API writes, cached reads, conflicts, retries, external side effects and callback completions can interleave. The adapter needs explicit scheduling/observation points or allowed stuttering/refinement steps. envtest alone does not reproduce a complete Kubernetes cluster or its controller set.

## CI direction

Fetch pinned inputs → extract descriptors → resolve typed bindings → generate Quint/LikeC4/adapter metadata → typecheck and check properties → replay traces against implementations → publish diagrams and evidence.

On a dependency update, review contract and generated diffs, then run conformance against the new version. A green structural compilation is not behavioral conformance.

References: [Quint model-based testing](https://quint.sh/docs/model-based-testing), [controller-gen](https://book.kubebuilder.io/reference/controller-gen.html), [envtest](https://book.kubebuilder.io/reference/envtest.html).

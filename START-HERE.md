# Start here: MAC behaviour, watches and the generated model

The current walkthrough is **`examples/assignment/`**, run through `cmd/walkthrough`. It includes both the MAC reconcile process and the watch registrations that cause MAC to run. The older `controller-demo` and publication examples are separate integration references.

The goal is to generate behaviour from the implementation, then check independent constraints. This example extracts supported Go decisions, watch registrations, predicates and a reference-to-key mapper. Effect meanings, the bounded runtime model and the replay adapter still have handwritten parts, identified below.

## Start with the controller setup

In `examples/assignment/wiring.go`, MAC watches InfraMachines as unstructured objects with an explicit GVK, and MachineClaims through their typed API:

```go
func (r *MAC) SetupWithManager(mgr ctrl.Manager) error {
	infra := &unstructured.Unstructured{}
	infra.SetGroupVersionKind(schema.GroupVersionKind{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "InfraMachine"})
	return ctrl.NewControllerManagedBy(mgr).
		For(infra).
		Watches(&apiv1.MachineClaim{}, handler.EnqueueRequestsFromMapFunc(ClaimToInfraMachine)).
		Complete(r)
}
```

`For` queues the InfraMachine's namespace/name. A MachineClaim event must queue the associated InfraMachine, so the second watch uses this mapper from the same file:

```go
func ClaimToInfraMachine(_ context.Context, obj client.Object) []reconcile.Request {
	claim, ok := obj.(*apiv1.MachineClaim)
	if !ok || claim.Spec.InfraMachineName == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: claim.Namespace, Name: claim.Spec.InfraMachineName}}}
}
```

The generator extracts the selected field, `Spec.InfraMachineName`, from this function. The example uses InfraMachine `demo/infra-1` and claim `demo/claim-for-infra-1`: their names differ, so incorrectly mapping the claim's own name is detectable.

MAC watches the MachineClaim **resource kind**. It does not register a new watch for each claim it creates. Creation and later status updates pass through the existing watch and mapper.

## Then follow one reconcile attempt

`examples/assignment/controller.go` contains the process called by the controller-runtime shell in `wiring.go`:

```go
func Reconcile(store Store) string {
	found := store.InfraMachineExists()
	if !found {
		return "absent"
	}
	compatible := store.SatisfiesMachineAssignmentContract()
	if !compatible {
		return "unsupported"
	}
	claimFound, machineID := store.MachineClaim()
	if !claimFound {
		store.CreateMachineClaim()
		return "waiting"
	}
	if machineID == "" {
		return "waiting"
	}
	store.UpdateInfraMachineStatus(machineID)
	return "assigned"
}
```

This is your four-step flow: see an InfraMachine, check the assignment contract, create or reuse a claim, then publish the fulfilled claim's machine ID. A `"waiting"` return ends the current attempt. A later watched event can queue another attempt; MAC does not block a worker waiting for fulfillment.

The `Store` interface is the example's I/O boundary, scoped to one InfraMachine. Its contract check reads a fixture compatibility annotation from the fetched unstructured InfraMachine. This miniature does not yet discover provider contracts or run the structural contract checker automatically. It also does not run the claim allocator or CAPI: `fulfillClaim` is an external environment action.

## What happens between attempts

The model separates object changes, watch delivery and queue processing:

| Step | Model action | Result |
| --- | --- | --- |
| An InfraMachine appears | `discoverCompatible` | Object exists; its event awaits delivery |
| Its event is delivered | `deliverInfra` | The InfraMachine key is queued |
| MAC runs | `reconcile`, followed by extracted Go steps | MAC creates one claim and returns `waiting` |
| The claim's creation event is delivered | `deliverClaim` | The reference maps back to the InfraMachine key |
| MAC runs again before fulfillment | `reconcile`, followed by Go steps | It reuses the pending claim and returns `waiting` |
| The claim subsystem assigns a machine | `fulfillClaim` | Claim status now contains `machine-1`; an update awaits delivery |
| The claim update is delivered | `deliverClaim` | The InfraMachine key is queued again |
| MAC runs again | `reconcile`, followed by Go steps | It publishes `machine-1` to InfraMachine status |

This is one possible schedule. Fulfillment may also happen before the claim's creation event is delivered, or while MAC is reconciling. Events for the same key coalesce in the queue. An event delivered while the key is being processed leaves a notification that causes another attempt after the current attempt returns.

Reconciliation requires a queued key. Merely changing an object does not start MAC. Publishing InfraMachine status can itself produce another event through the InfraMachine watch.

## What is extracted, and what is still supplied

| Input | What it supplies |
| --- | --- |
| `controller.go` | **Extracted:** branches, operation order, publication argument and return paths |
| `wiring.go` | **Extracted:** `For`/`Watches` registrations, supported predicates and the mapper's reference field |
| `process.yaml` | **Authored effect summaries:** what Store reads and writes mean in model state |
| `model.yaml` | **Authored domain:** initial state, effect identities and external actions such as discovery and fulfillment |
| `watches.go` | **Handwritten integration:** typed-resource identities, fixed object keys, event/queue semantics, and composition with extracted watch rules |
| `example.go` | **Handwritten integration:** selection of the process, Store-call summaries, bounded scheduling and the Go replay adapter |
| `dynamic_store.go` | **Extracted access dependencies:** dynamic-client GVR expressions and API verbs; this is also the real Store implementation used in replay |
| `resources.yaml` | **Declared runtime bindings:** GVR, GVK and scope, without provider Go types |
| `dynamic_adapter.go` | **Handwritten fixture:** fake dynamic API, injected observed client and independent state projection |
| `constraints.yaml` | **Independent requirements:** conditions that generated behaviour must satisfy |

These paths are under `examples/assignment/`. The reusable extraction frontends live in `pkg/goflow`, `pkg/process`, `pkg/watchgen` and `pkg/dynamicgen`.

The generator does not infer an entire runtime from controller-runtime source. The watch rules parameterize a declared one-key queue model. Replay checks that model against selected native event handlers and the existing real controller-runtime worker/client-go FIFO harness. The model is bounded to one InfraMachine, one claim, one assigned machine value and four reconcile invocations; the invocation cap is implemented in the Go integration.

During replay, the actual process in `controller.go` runs against `DynamicStore`, wrapped at the controlled Store boundaries. It performs real dynamic-client calls against an in-memory fake API; the fixture registers no provider Go types. Projection reads the API contents independently. The adapter selects native handler/predicate components using the extracted rules. A live manager, API server and informer are not started, and registrations are not independently captured from a running manager. The `MAC.Reconcile` shell is compiled but the replay harness invokes the inner process directly.

## Dynamic resource access

`dynamic_store.go` uses runtime GVRs for resource access. For example:

```go
obj, err := s.Client.Resource(s.InfraResource).
    Namespace(s.Key.Namespace).
    Get(s.Context, s.Key.Name, metav1.GetOptions{})
```

`pkg/dynamicgen` extracts those resource expressions and operations from selected methods. `resources.yaml` supplies a bounded binding, standing in for CRD/discovery data:

```yaml
apiVersion: architecture.local/v1alpha1
kind: KubernetesResourceCatalog
metadata:
  name: assignment-api
spec:
  bindings:
    - expression: s.InfraResource
      resource: {group: demo.architecture.local, version: v1alpha1, kind: InfraMachine}
      plural: inframachines
      scope: Namespaced
```

The complete file also binds MachineClaim. Plural resource names and namespace scope are explicit; the generator does not guess them from a Go type. Extracted accesses are checked against the process's effect summaries. Changing an API verb or target resource cannot silently keep the old summary.

`Observer.Dynamic` observes `Get`, `Create`, `Update` and `UpdateStatus` with zero options. It checks the requested GVR against the mapper, validates object GVK/namespace/name, and captures detached snapshots. Other operations, including List, Watch transport, Patch and Apply, currently fail closed. `Observer.Client` also accepts unstructured objects with explicit GVKs without requiring a typed Scheme.

Dynamic clients and dynamically registered watches are separate concerns. The watch frontend supports the shown unstructured declaration and `SetGroupVersionKind`, plus explicit bindings for runtime GVK expressions through `ExtractWithGVKs`. It does not yet discover arbitrary registration loops or import discovery data automatically. The dynamic access frontend extracts dependencies, not every branch inside the Store; effect semantics remain explicit.

## Keep the requirements independent

`constraints.yaml` is KRM YAML with CEL expressions. For example:

```yaml
apiVersion: architecture.local/v1alpha1
kind: ModelConstraints
metadata:
  name: assignment-requirements
spec:
  properties:
    - name: publicationRequiresFulfilledClaim
      expression: 'statusMachineId == "" || (claimExists && claimMachineId != "" && statusMachineId == claimMachineId)'
    - name: claimFulfillmentEnqueuesMAC
      expression: 'event != "deliverClaim" || claimMachineId == "" || queued'
```

The file has additional constraints for contract compatibility, duplicate claims, result consistency, InfraMachine wake-ups and correct target keys. These requirements stay unchanged when implementation source changes.

The source-mutation tests demonstrate why this matters:

| Change to Go | Consequence detected by an unchanged constraint |
| --- | --- |
| Remove the MachineClaim watch | Delivery of a fulfilled claim event fails to queue MAC |
| Map `claim.Name` instead of `claim.Spec.InfraMachineName` | The watch targets the wrong reconcile key |
| Add `GenerationChangedPredicate` to the claim watch | Status-only fulfillment is filtered out, losing the required wake-up |
| Invert a contract/fulfillment check or publish the wrong ID | The resulting claim/publication violates the assignment requirements |

The fixture's fulfillment update changes resource version while leaving generation unchanged. `ResourceVersionChangedPredicate` permits that update. Both supported predicates are checked against the native controller-runtime implementations in replay.

These constraints check safety and wake-up behaviour **when an event is delivered**. They do not prove that delivery or scheduling eventually occurs.

## Run the current example

From `~/dev/generative-architecture`, with Go 1.25 or newer:

```sh
go run ./cmd/walkthrough
go test ./examples/assignment
```

The command regenerates the process, watch descriptors, Quint model and LikeC4 sources, then checks five scenarios: absent, unsupported, pending, fulfilled and repeated. Its terminal output summarizes final outcomes; the event-by-event behaviour is in the generated model and the schedules above. Kubernetes, Quint and Node are not needed for this run.

To run just the watch checks with visible test names:

```sh
go test ./examples/assignment -run 'Test(NoReconcileWithoutDeliveredWatch|MissingWatchAndWrongMappingChangeModel|StatusPredicateMatchesNativeHandler|EventDuringReconcileSurvivesReturn)' -v
```

The mutation tests **pass when they detect the intended violation**. They modify source in memory, leaving the example files intact.

For fresh Quint exploration, with the separately installed Quint CLI on PATH or selected by `QUINT_BIN`:

```sh
go run ./cmd/walkthrough --refresh
```

This samples 40 traces of up to 60 steps, replays them against Go, and updates `examples/assignment/traces/` and `report.yaml`. Native tests validate the saved evidence hashes. Rerun the walkthrough after changing its source; the current `make generate` target refreshes the other examples and does not include this walkthrough.

## Find the generated results

All of these are under `examples/assignment/generated/`:

| File | What to inspect |
| --- | --- |
| `dynamic-client.yaml` | Dynamic access methods, GVR/GVK bindings, API verbs, source lines and hashes |
| `dynamic-client.c4` | Resource dependencies extracted from dynamic-client calls |
| `watches.yaml` | Extracted registrations, mapper field paths, predicates, source lines and hashes |
| `process.yaml` | Extracted control-flow graph and source evidence |
| `Model.qnt` | Combined process/event/queue behaviour and independent constraints |
| `watches.c4` | Resource events and their routes to the MAC queue |
| `process.c4` | Branches, calls and returns extracted from Go |
| `architecture.c4` | Resource dependencies from the declared effect summaries |

For the **current walkthrough diagrams**, point LikeC4 at this example's generated directory:

```sh
likec4 start examples/assignment/generated
```

Use the executable selected by `LIKEC4_BIN` instead of `likec4` if needed. The root `arch render` / `arch serve` commands show the larger machine-vendor example; they do not currently include these walkthrough views.

## What remains toward a generated system architecture

The current example extracts a supported builder setup with an optional unstructured-object/GVK preamble, a local same-namespace field-reference mapper, and generation/resource-version predicates. Custom handlers or predicates, `Owns`, dynamic registrations and arbitrary mapper control flow fail extraction. Arbitrary Go helper semantics also need summaries or additional frontend support.

The next composition work is to replace the external fulfillment action with the claim subsystem's generated process and generalize the example-specific resource/effect/queue bindings. Structural contracts, automatic unambiguous port matching and broader runtime profiles exist elsewhere in the library, but this walkthrough does not yet assemble all of them automatically.

API errors, full informer/cache behaviour, lease revocation, multiple MAC keys/workers and upstream CAPI conformance are outside this miniature. Some are exercised separately by the larger integration examples. See [LIBRARY.md](LIBRARY.md) for the public APIs and their current limits.

The delivery roadmap is in [PROJECT-PLAN.md](PROJECT-PLAN.md).

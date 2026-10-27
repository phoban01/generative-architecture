# Start here: the sample controller's behaviour, watches and the generated model

The current walkthrough is **`examples/database/`**, run through `cmd/walkthrough`. It includes both the controller's reconcile process and the watch registrations that cause it to run. `examples/configmirror/` is the first source-generated controller: its model, replay projection, environment and boundary gating are all generated from its source, an abstraction document and the built-in client summaries. The runtime group under `examples/` is a separate integration reference.

The goal is to generate behaviour from the implementation, then check independent constraints. This example extracts supported Go decisions, watch registrations, predicates and a reference-to-key mapper. Effect meanings, the bounded runtime model and the replay adapter still have handwritten parts, identified below.

## Start with the controller setup

In `examples/database/wiring.go`, the controller watches Databases as unstructured objects with an explicit GVK, and DatabaseInstances through their typed API:

```go
func (r *DatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	database := &unstructured.Unstructured{}
	database.SetGroupVersionKind(schema.GroupVersionKind{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "Database"})
	return ctrl.NewControllerManagedBy(mgr).
		For(database).
		Watches(&apiv1.DatabaseInstance{}, handler.EnqueueRequestsFromMapFunc(InstanceToDatabase)).
		Complete(r)
}
```

`For` queues the Database's namespace/name. A DatabaseInstance event must queue the associated Database, so the second watch uses this mapper from the same file:

```go
func InstanceToDatabase(_ context.Context, obj client.Object) []reconcile.Request {
	instance, ok := obj.(*apiv1.DatabaseInstance)
	if !ok || instance.Spec.DatabaseName == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: instance.Namespace, Name: instance.Spec.DatabaseName}}}
}
```

The generator extracts the selected field, `Spec.DatabaseName`, from this function. The example uses Database `demo/db-1` and instance `demo/instance-for-db-1`: their names differ, so incorrectly mapping the instance's own name is detectable.

The controller watches the DatabaseInstance **resource kind**. It does not register a new watch for each instance it creates. Creation and later status updates pass through the existing watch and mapper.

## Then follow one reconcile attempt

`examples/database/controller.go` contains the process called by the controller-runtime shell in `wiring.go`:

```go
func Reconcile(store Store) string {
	found := store.DatabaseExists()
	if !found {
		return "absent"
	}
	compatible := store.SatisfiesProvisioningContract()
	if !compatible {
		return "unsupported"
	}
	instanceFound, endpoint := store.DatabaseInstance()
	if !instanceFound {
		store.CreateDatabaseInstance()
		return "waiting"
	}
	if endpoint == "" {
		return "waiting"
	}
	store.UpdateDatabaseStatus(endpoint)
	return "ready"
}
```

This is your four-step flow: see a Database, check the provisioning contract, create or reuse an instance, then publish the provisioned instance's endpoint. A `"waiting"` return ends the current attempt. A later watched event can queue another attempt; the controller does not block a worker waiting for provisioning.

The `Store` interface is the example's I/O boundary, scoped to one Database. Its contract check reads a fixture compatibility annotation from the fetched unstructured Database. This miniature does not yet discover provider contracts or run the structural contract checker automatically. It also does not run the provisioning subsystem: `provisionInstance` is an external environment action.

## What happens between attempts

The model separates object changes, watch delivery and queue processing:

| Step | Model action | Result |
| --- | --- | --- |
| A Database appears | `discoverCompatible` | Object exists; its event awaits delivery |
| Its event is delivered | `deliverDatabase` | The Database key is queued |
| The controller runs | `reconcile`, followed by extracted Go steps | It creates one instance and returns `waiting` |
| The instance's creation event is delivered | `deliverInstance` | The reference maps back to the Database key |
| The controller runs again before provisioning | `reconcile`, followed by Go steps | It reuses the pending instance and returns `waiting` |
| The provisioning subsystem fills in the endpoint | `provisionInstance` | Instance status now contains `db-1.internal`; an update awaits delivery |
| The instance update is delivered | `deliverInstance` | The Database key is queued again |
| The controller runs again | `reconcile`, followed by Go steps | It publishes `db-1.internal` to Database status |

This is one possible schedule. Provisioning may also happen before the instance's creation event is delivered, or while the controller is reconciling. Events for the same key coalesce in the queue. An event delivered while the key is being processed leaves a notification that causes another attempt after the current attempt returns.

Reconciliation requires a queued key. Merely changing an object does not start the controller. Publishing Database status can itself produce another event through the Database watch.

## What is extracted, and what is still supplied

| Input | What it supplies |
| --- | --- |
| `controller.go` | **Extracted:** branches, operation order, publication argument and return paths |
| `wiring.go` | **Extracted:** `For`/`Watches` registrations, supported predicates and the mapper's reference field |
| `process.yaml` | **Authored effect summaries:** what Store reads and writes mean in model state |
| `abstraction.yaml` | **Declared abstraction:** selected fields, value domains, identity keys and references. `generate.go` builds the model, its effects and its environment actions from it, from `process.yaml` and from the extracted process |
| `native_delivery.go` | **Handwritten replay glue:** delivers model events through the real controller-runtime handlers and predicates selected by the extracted rules. The event and queue state itself is generated by `pkg/watchmodel` from those rules and the abstraction |
| `example.go` | **Handwritten integration:** selection of the process, Store-call summaries, bounded scheduling and the Go replay adapter |
| `dynamic_store.go` | **Extracted access dependencies:** dynamic-client GVR expressions and API verbs; this is also the real Store implementation used in replay |
| `resources.yaml` | **Declared runtime bindings:** GVR, GVK and scope, without provider Go types |
| `dynamic_adapter.go` | **Handwritten fixture:** fake dynamic API, injected observed client and independent state projection |
| `constraints.yaml` | **Independent requirements:** conditions that generated behaviour must satisfy |

These paths are under `examples/database/`. The reusable extraction frontends live in `pkg/goflow`, `pkg/process`, `pkg/watchgen` and `pkg/dynamicgen`.

The generator does not infer an entire runtime from controller-runtime source. The watch rules parameterize a declared one-key queue model. Replay checks that model against selected native event handlers and the existing real controller-runtime worker/client-go FIFO harness. The model is bounded to one Database, one instance, one endpoint value and four reconcile invocations; the invocation cap is implemented in the Go integration.

During replay, the actual process in `controller.go` runs against `DynamicStore`, wrapped at the controlled Store boundaries. It performs real dynamic-client calls against an in-memory fake API; the fixture registers no provider Go types. Projection reads the API contents independently. The adapter selects native handler/predicate components using the extracted rules. A live manager, API server and informer are not started, and registrations are not independently captured from a running manager. The `DatabaseReconciler.Reconcile` shell is compiled but the replay harness invokes the inner process directly.

## Dynamic resource access

`dynamic_store.go` uses runtime GVRs for resource access. For example:

```go
obj, err := s.Client.Resource(s.DatabaseResource).
    Namespace(s.Key.Namespace).
    Get(s.Context, s.Key.Name, metav1.GetOptions{})
```

`pkg/dynamicgen` extracts those resource expressions and operations from selected methods. `resources.yaml` supplies a bounded binding, standing in for CRD/discovery data:

```yaml
apiVersion: architecture.local/v1alpha1
kind: KubernetesResourceCatalog
metadata:
  name: database-api
spec:
  bindings:
    - expression: s.DatabaseResource
      resource: {group: demo.architecture.local, version: v1alpha1, kind: Database}
      plural: databases
      scope: Namespaced
```

The complete file also binds DatabaseInstance. Plural resource names and namespace scope are explicit; the generator does not guess them from a Go type. Extracted accesses are checked against the process's effect summaries. Changing an API verb or target resource cannot silently keep the old summary.

`Observer.Dynamic` observes `Get`, `Create`, `Update` and `UpdateStatus` with zero options. It checks the requested GVR against the mapper, validates object GVK/namespace/name, and captures detached snapshots. Other operations, including List, Watch transport, Patch and Apply, currently fail closed. `Observer.Client` also accepts unstructured objects with explicit GVKs without requiring a typed Scheme.

Dynamic clients and dynamically registered watches are separate concerns. The watch frontend supports the shown unstructured declaration and `SetGroupVersionKind`, plus explicit bindings for runtime GVK expressions through `ExtractWithGVKs`. It does not yet discover arbitrary registration loops or import discovery data automatically. The dynamic access frontend extracts dependencies, not every branch inside the Store; effect semantics remain explicit.

## Keep the requirements independent

`constraints.yaml` is KRM YAML with CEL expressions. For example:

```yaml
apiVersion: architecture.local/v1alpha1
kind: ModelConstraints
metadata:
  name: database-requirements
spec:
  properties:
    - name: publicationRequiresProvisionedInstance
      expression: 'statusEndpoint == "" || (instanceExists && instanceEndpoint != "" && statusEndpoint == instanceEndpoint)'
    - name: instanceProvisioningEnqueuesReconciler
      expression: 'event != "deliverInstance" || instanceEndpoint == "" || queued'
```

The file has additional constraints for contract compatibility, duplicate instances, result consistency, Database wake-ups and correct target keys. These requirements stay unchanged when implementation source changes.

The source-mutation tests demonstrate why this matters:

| Change to Go | Consequence detected by an unchanged constraint |
| --- | --- |
| Remove the DatabaseInstance watch | Delivery of a provisioned instance event fails to queue the controller |
| Map `instance.Name` instead of `instance.Spec.DatabaseName` | The watch targets the wrong reconcile key |
| Add `GenerationChangedPredicate` to the instance watch | Status-only provisioning is filtered out, losing the required wake-up |
| Invert a contract/provisioning check or publish the wrong endpoint | The resulting instance/publication violates the provisioning requirements |

The fixture's provisioning update changes resource version while leaving generation unchanged. `ResourceVersionChangedPredicate` permits that update. Both supported predicates are checked against the native controller-runtime implementations in replay.

These constraints check safety and wake-up behaviour **when an event is delivered**. They do not prove that delivery or scheduling eventually occurs.

## Run the current example

From `~/dev/generative-architecture`, with Go 1.25 or newer:

```sh
go run ./cmd/walkthrough
go test ./examples/database
```

The command regenerates the process, watch descriptors, Quint model and LikeC4 sources, then checks five scenarios: absent, unsupported, pending, provisioned and repeated. Its terminal output summarizes final outcomes; the event-by-event behaviour is in the generated model and the schedules above. Kubernetes, Quint and Node are not needed for this run.

To run just the watch checks with visible test names:

```sh
go test ./examples/database -run 'Test(NoReconcileWithoutDeliveredWatch|MissingWatchAndWrongMappingChangeModel|StatusPredicateMatchesNativeHandler|EventDuringReconcileSurvivesReturn)' -v
```

The mutation tests **pass when they detect the intended violation**. They modify source in memory, leaving the example files intact.

For fresh Quint exploration, with the separately installed Quint CLI on PATH or selected by `QUINT_BIN`:

```sh
go run ./cmd/walkthrough --refresh
```

This samples 40 traces of up to 60 steps, replays them against Go, and updates `examples/database/traces/` and `report.yaml`. Native tests validate the saved evidence hashes. Rerun the walkthrough after changing its source; `make verify` refreshes every example, including this one.

## Find the generated results

All of these are under `examples/database/generated/`:

| File | What to inspect |
| --- | --- |
| `dynamic-client.yaml` | Dynamic access methods, GVR/GVK bindings, API verbs, source lines and hashes |
| `watches.yaml` | Extracted registrations, mapper field paths, predicates, source lines and hashes |
| `process.yaml` | Extracted control-flow graph and source evidence |
| `Model.qnt` | Combined process/event/queue behaviour and independent constraints |
| `architecture.c4` | One LikeC4 workspace: the watches and API effects (resource view) and the extracted control flow (process view) |

For the **current walkthrough diagrams**, point LikeC4 at this example's generated directory:

```sh
likec4 start examples/database/generated
```

Use the executable selected by `LIKEC4_BIN` instead of `likec4` if needed. `make render` builds the same directory into `site/`.

## What remains toward a generated system architecture

The current example extracts a supported builder setup with an optional unstructured-object/GVK preamble, a local same-namespace field-reference mapper, and generation/resource-version predicates. Custom handlers or predicates, `Owns`, dynamic registrations and arbitrary mapper control flow fail extraction. Arbitrary Go helper semantics also need summaries or additional frontend support.

The next composition work is to replace the external provisioning action with the provisioning subsystem's generated process and generalize the example-specific resource/effect/queue bindings. Structural contracts, automatic unambiguous port matching and broader runtime profiles exist elsewhere in the library, but this walkthrough does not yet assemble all of them automatically.

API errors, full informer/cache behaviour, instance deprovisioning, multiple keys/workers and upstream project conformance are outside this sample. Some are exercised separately by the other examples. See [LIBRARY.md](LIBRARY.md) for the public APIs and their current limits.

The delivery roadmap is in [PROJECT-PLAN.md](PROJECT-PLAN.md).

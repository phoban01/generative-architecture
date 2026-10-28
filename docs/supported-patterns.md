# Supported controller construction and dependency-injection patterns

Status: Checkpoint A (2026-10-14); map functions and `Reconcile` added
2026-10-28 (#103). Applies to `sourcegen.Discover` and the frontends that
build on it.

A reconciler is any named struct type whose pointer implements
`reconcile.Reconciler` from controller-runtime. Discovery needs no marker;
`+arch:controller=<name>` only renames a controller and is required only when
two packages declare reconcilers of the same type name.

The extractor must know, from the source alone, where a reconciler's client
comes from. Clients obtained any other way cannot be bounded or replaced
during replay, so the patterns below are the whole supported set: a reconciler
constructed another way is reported with a located diagnostic, never modelled
by guesswork. Project-specific setup must not be hidden in a library helper.

## Supported

### Exported struct fields

Dependencies are exported fields of the reconciler struct. Each field of one
of these types is recorded in the controller's `injection` list:

| Field type | Role |
| --- | --- |
| `sigs.k8s.io/controller-runtime/pkg/client.Client` | cached read/write client |
| `sigs.k8s.io/controller-runtime/pkg/client.Reader` | uncached API reader |
| `k8s.io/client-go/dynamic.Interface` | dynamic client |
| `k8s.io/client-go/tools/record.EventRecorder` | event recorder |
| any interface declared in the module under analysis | project boundary (a store, a provider) |

Embedding counts as a field: `type Reconciler struct { client.Client }` injects
`Client`. Every reconciler must receive at least one of the three client
types; an event recorder or a project interface alone does not give it API
access. Other fields (scheme, options, a key to act on) are ordinary values
and are not injection.

```go
type Reconciler struct {
	Client    client.Client
	APIReader client.Reader
	Recorder  record.EventRecorder
	Store     Store // declared in this module
}
```

### Constructor functions

A function in the same package may build the reconciler, filling the fields
above from the manager: `NewReconciler(mgr ctrl.Manager) *Reconciler` or a
literal `&Reconciler{Client: mgr.GetClient()}` in `main`. The constructor is
where the manager's getters belong; the fields it sets are what discovery
records.

### `SetupWithManager(mgr)` with the builder

Watches are registered by a method declared on the reconciler type itself:

```go
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&v1.Database{}).Complete(r)
}
```

It takes exactly one controller-runtime `Manager` (`ctrl.Manager` or
`manager.Manager`) and ends in `Complete(r)`. Its body is read by `watchgen`,
which accepts a builder chain and the unstructured-object preamble documented
there. A method named `SetupWithManager` with another signature, or one
promoted from an embedded base type, is an error: a shared base would hide the
project's wiring.

### Map functions

`Watches(&T{}, handler.EnqueueRequestsFromMapFunc(f))` accepts a package
function or a method of the reconciler, declared in the setup file, in one
of three shapes. The IR records each as a key mapping.

| Shape | Mapping |
| --- | --- |
| Assert the object to `*T`, return nil for an empty field, return one key from that field | `field` |
| List the dependents in the object's namespace with `client.MatchingLabels{key: obj.GetName()}`, one key per item | `label` |
| List the dependents in the object's namespace, one key per item whose field equals `obj.GetName()` | `reference` |

The list shapes are fixed:

```go
func (r *Reconciler) tenantToDatabases(ctx context.Context, obj client.Object) []reconcile.Request {
	databases := &v1.DatabaseList{}
	if err := r.Client.List(ctx, databases, client.InNamespace(obj.GetNamespace()), client.MatchingLabels{TenantLabel: obj.GetName()}); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(databases.Items))
	for _, db := range databases.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: db.Namespace, Name: db.Name}})
	}
	return requests
}
```

The label key must be a string constant. A reference mapping drops the
label selector and guards the `append` with `if item.Spec.Ref == obj.GetName()`.
Watched and listed types resolve to catalog entries by the group and
version of their Go package: a `GroupVersion` or `SchemeGroupVersion`
variable, or a `+groupName` marker with the package name as the version.

## Reconcile

`arch extract` reads every `Reconcile` method with the typed frontend
(ADR-0008). It needs no abstraction and no controller declaration. Besides
branches, assignments, `switch` and early returns, the frontend reads:

| Construct | Reading |
| --- | --- |
| A call of a controller-runtime client method, also one promoted through an embedded `client.Client` | a client operation (`get`, `list`, `create`, `update`, `patch`, `delete`, or `status.` and the operation) |
| `apierrors.IsNotFound(err)` and the other error idioms | an outcome of the call that set `err` |
| `controllerutil.SetControllerReference(owner, obj, scheme)` and `SetOwnerReference` | an in-memory write of the owner reference fields; options are not summarized |
| `return reconcile.Result{Requeue: true}, nil` or `{RequeueAfter: d}` | a return that asks for a requeue; `Requeue` must be a constant, `d` may be any expression |
| `resource.MustParse("10")` and `resource.NewQuantity` with constant arguments | an opaque quantity value |
| `"db" + "-credentials"` with constant operands | a string constant |
| A slice or struct literal assigned to a field that no abstraction selects, such as a Job's containers | a write without a model value |
| A map literal with constant keys assigned to a field | one write per key; with an abstraction, its selected keys that the literal omits become absent |

Without an abstraction, the support report lists every object field that
`Reconcile` reads or writes as `unresolved`, at its first use. With an
abstraction, only the fields it does not select are listed. Verification
mode (`--strict`) fails on them, so a controller needs an abstraction
before strict mode passes.

## Unsupported

Each of these produces a diagnostic with a `file:line` location. The
controllers that were found are still returned so a support report can list
them.

| Pattern | Diagnostic |
| --- | --- |
| A reconciler with no field of a client type | `injection` at the type declaration |
| A package-level variable of a client type (`var c client.Client`) | `package-client` at the variable |
| `mgr.GetClient()`, `mgr.GetAPIReader()` or `mgr.GetEventRecorderFor()` called inside `SetupWithManager`, including captured in closures | `setup-client` at the call |
| A client built inside `Reconcile`, or obtained through reflection or a global registry | `injection`, since no field carries it |
| Setup promoted from an embedded helper type | error from discovery |
| A string concatenation with a field or variable, such as `name + "-credentials"` | `expression` at the concatenation |
| A call in `Reconcile` that no summary covers, such as a logger | `call` at the statement |
| A map function in another shape than the three above | `mapper` at the function |

Function-typed fields that return a client or a project interface (factories
such as `StoreFor func(ctx, key) Store`) are not injection: the extractor
cannot see what the function returns at runtime, so the reconciler is reported
as receiving no client.

## Where it is enforced

- `sourcegen.Discover` returns the controllers and the diagnostics;
  `sourcegen.DiscoverControllers` fails closed by returning the diagnostics
  joined as its error.
- `watchgen` rejects setup bodies outside the builder subset.
- `goflow` rejects reconcile bodies that use constructs outside its subset.

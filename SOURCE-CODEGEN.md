# Go source → architecture, with small markers

Run commands from the repository root.

Markers identify code. Package context supplies API metadata. YAML describes the semantic joins. Existing behavior contracts supply protocol names, directions, identity keys and expected behavior. Those facts are not repeated in controller comments.

## 1. Opt in an API type

In `examples/machinevendor/api/v1alpha1/types.go`:

```go
// +groupName=demo.architecture.local
package v1alpha1

// +arch:resource
type InfraMachine struct {
    // Kubernetes metadata omitted here for brevity.
    Spec   InfraMachineSpec   `json:"spec"`
    Status InfraMachineStatus `json:"status"`
}
```

This yields GVK `demo.architecture.local/v1alpha1, Kind=InfraMachine`: group from the standard package marker, version from the package name, kind from the Go type name. A package-level `+versionName` override is also supported. No GVK is repeated on each resource.

Standard `+kubebuilder:object:root=true` markers also appear in the real file because stock controller-gen generates its Kubernetes deepcopy methods. `+arch:resource` separately opts the type into architecture extraction. The demo's fake-client scheme uses these typed resources; they are not disconnected documentation structs.

## 2. Identify the controller and its action methods

In `examples/machinevendor/controllerdemo/mac.go`:

```go
// +arch:controller=mac
type MACReconciler struct {
    // Client, APIReader and the deliberate mutation flag.
}

// +arch:action=publish
func (r *MACReconciler) Publish(
    ctx context.Context,
    key client.ObjectKey,
    observed Assignment,
) (bool, error) {
    // Independently written implementation.
}
```

The generator obtains the Go package, receiver type, method name, signature and source location. It checks the controller's `Reconcile` signature. The action label is the intentional semantic link to `publish` in MAC's existing contract. Renaming `Publish` while retaining its comment updates the extracted method name; no method name is repeated in configuration. Actual call sites still have to compile.

## 3. Put composition in YAML

`source-mappings.yaml` contains the relationships that Go types alone cannot establish:

```yaml
apiVersion: architecture.local/v1alpha1
kind: SourceBindings
metadata:
  name: mac
spec:
  controllers:
    mac:
      primary: InfraMachine
      owns: MachineClaim
      ports:
        eligible: InfraMachine.Spec.AllocationRequested
        claim: MachineClaim.$exists
        lease: MachineClaim.Status
        assignment: InfraMachine.Status.MachineID
```

The compiler joins `mac` to the component already selected in `system.yaml`, then reuses that component's existing contract. For example, the `eligible` protocol and `inframachine-uid` identity key come from `contracts/mac.yaml`. They are not restated in the marker or mapping.

`$exists` means that presence of the mapped object projects to a boolean. The grouping by InfraMachine identity is a declared semantic mapping. The actual adapter still implements the identity lookup; extraction does not prove that ownership references or runtime watches enforce it.

## 4. Run generation

```sh
# Only needed after API changes that affect generated deepcopy methods:
make api-generate

# Read Go source and write the structural descriptor:
go run ./cmd/arch extract

# Extract, validate against contracts, then generate model, diagrams and dispatch:
go run ./cmd/arch generate

# Replay saved model traces against the demo controller:
go run ./cmd/controller-demo
```

`system.yaml` selects the source inputs:

```yaml
sources:
  packages:
    - ./examples/machinevendor/api/v1alpha1
    - ./examples/machinevendor/controllerdemo
  mappings: source-mappings.yaml
  descriptor: generated/source-descriptors.yaml
```

Extraction uses `go/packages`, `go/types` and controller-tools v0.20.1's marker parser. It requires Go on PATH and resolvable module dependencies, including when called through the native `arch` binary. Replay can use the saved descriptor without loading Go packages; it verifies source hashes first.

## 5. Inspect the outputs

The generated `source-descriptors.yaml` contains this projection for MAC's eligibility input:

```yaml
name: eligible
resource: generative-architecture/examples/machinevendor/api/v1alpha1.InfraMachine
goField: Spec.AllocationRequested
jsonPath: spec.allocationRequested
goType: bool
type: map(string, bool)
evidence: semantic join declared in YAML; field path and type checked from Go
```

It also records resource GVKs, method signatures, source file/line locations and source/mapping/module hashes. `generated/architecture.yaml` combines this with the selected contracts, adding their protocol, direction and identity information. `generated/source-structure.c4` supplies a separate `code_structure` view. The original behavior diagram remains separate.

The remaining generated outputs are `System.qnt`, `architecture.c4`, `adapter_generated.go` and MBT metadata. Expected behavior remains authored YAML/CEL; extraction never rewrites it from the implementation.

## A concrete drift check

Change `AllocationRequested bool` to `AllocationRequested string` in an isolated copy and run generation. Go source still type-checks in this demo, but architecture compilation rejects it:

```text
source port mac.eligible incompatible with contract
(extracted map(string, string), contract map(string, bool))
```

This is the benefit over an unchecked config file: the mapping is resolved against actual symbols and types on every generation. Renaming a mapped Go field without updating the YAML fails with a missing-field error. Removing an action marker fails contract action coverage. Unknown or misplaced architecture markers fail extraction. Modified, added or removed source files invalidate the saved descriptor.

## Scope

The current extractor covers explicitly selected local packages, named resource structs, explicit JSON-tagged field paths, scalar values and the existing Assignment record shape. It expects unique short resource names across selected packages. Pointer/optional values, arbitrary records and collections need additional model semantics and currently fail when used as port values. Kubernetes embedded metadata is omitted from the displayed field inventory.

GVKs follow package/type conventions; the tool does not inspect custom runtime scheme registration. Primary/owned relationships are declared in YAML, not inferred from `SetupWithManager`. Action markers identify implementation methods, but do not generate their scheduling or prove their behavior. The controller adapter and projection remain independently handwritten and tested. Remote repository fetching and pinned upstream adapters remain future work.

For an upstream project you cannot annotate, a YAML symbol-selection mode would be a natural extension. This prototype currently requires markers on the selected resources, controller and action methods.

References: [controller-tools marker parser](https://pkg.go.dev/sigs.k8s.io/controller-tools/pkg/markers), [Go package loader](https://pkg.go.dev/golang.org/x/tools/go/packages), [controller-gen](https://book.kubebuilder.io/reference/controller-gen.html).

The extractor is now public as `pkg/sourcegen`. Flat named Go records are emitted with their JSON field schema in `SourceDescriptor.status.records`; the machine-vendor consumer checks these against its domain schema. No special `Assignment` recognition remains in the library. Process-body extraction is still the separately documented MAC-specific frontend.

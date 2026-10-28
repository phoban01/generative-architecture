# Adopting arch for an upstream controller

This guide is for a controller-runtime project that you run but do not
change: an upstream controller such as Flux, a Cluster API provider or a
kubebuilder sample. The source stays byte-identical to the upstream
release. For a project you own, read
[adopt-owned-project.md](adopt-owned-project.md).

There are two paths:

- **Extract it.** Pin the source, run `arch extract` and read the support
  report. This works only when the controller stays inside the supported
  subset ([supported-patterns.md](supported-patterns.md)).
- **Describe it.** When extraction is not possible, write a `BoundaryModel`
  that declares what the controller watches, writes and promises. Check
  provider types against `InteractionContract`s. Compose the boundary with
  your owned controllers.

The example is the kubebuilder CronJob tutorial controller, pinned under
`testdata/external/cronjob` (Checkpoint B,
[ADR-0008](adr/0008-checkpoint-b-findings.md)). Extraction fails closed on
it today. The boundary examples are the Flux and Cluster API documents
under `pkg/boundary/library`.

`TestUpstreamProjectGuideWalk` in `cmd/arch/guide_test.go` runs every
command in this guide on a copy of the pinned controller, with
`GOPROXY=off`. `TestUpstreamProjectGuideBoundary` loads the documents this
guide shows and runs the Go calls it describes.

## 1. Pin the upstream source

Copy the upstream files you need into a directory of your repository, such
as `testdata/external/<name>` or `third_party/<name>`. Do not edit them.
`testdata/external/cronjob` shows the layout:

- `PIN.md` records the repository, path, tag, commit, licence, the date of
  the copy and what was left out.
- `upstream.sha256` lists each copied file with its SHA-256.
- `go.mod` and `go.sum` make the directory its own module. They keep the
  upstream module path. A directory under `testdata` is not part of the
  outer module's `./...`.

Check the copy against the pin, and check that the module builds from the
local module cache:

```sh
sha256sum -c upstream.sha256
GOWORK=off go build ./...
```

`TestExternalCronJobPinned` runs the same hash check in `go test`, so an
edit to the upstream source fails the build. The CronJob pin moved
controller-runtime from v0.22.1 to v0.23.3, so that the module builds from
the cache this repository already has. The source built unchanged. arch
matches client calls by package path, not by version.

## 2. Write the project document

In the pinned directory, run `arch init` with the upstream module path, the
controller package and the API package:

```sh
arch init --module tutorial.kubebuilder.io/project \
  --package ./internal/controller --package ./api/v1 --name cronjob
```

It writes `project.yaml` and an empty `constraints.yaml`. It writes no
catalog, abstraction or controller declarations.

## 3. Add a catalog from CRDs and a discovery snapshot

Name the upstream CRD manifests and a discovery snapshot for the built-in
kinds:

```yaml
spec:
  catalog:
    crds:
      - config/crd/bases/batch.tutorial.kubebuilder.io_cronjobs.yaml
    discovery:
      - catalog/batch-v1.json
```

A discovery snapshot is a JSON list of `APIResourceList` documents. Save
the output of `kubectl get --raw /apis/<group>/<version>` (or `/api/v1`)
and wrap it in `[ ]`.

A Go type matches a catalog entry by kind and by the name of its Go
package, which must equal the version. Upstream projects often reuse a
kind name. The CronJob API package is named `v1`, and so is
`k8s.io/api/batch/v1`. The full `batch/v1` snapshot also lists a
`CronJob`, so the match would be ambiguous. The CronJob snapshot keeps only
the `Job` resource.

## 4. Read the support report

```sh
arch extract
```

It prints:

```text
arch extract: unsupported constructs: 54 found, listed in generated/support.yaml
```

`arch extract` writes `generated/support.yaml` and no `ir.yaml`, because a
construct is unsupported. It reads `SetupWithManager` and `Reconcile` with
no abstraction. A missing catalog does not show here: extraction stops at
the setup preamble before it reads any watch.

`arch support` extracts in memory and prints the report as a table:

```sh
arch support
```

```text
STATUS       KIND         CONSTRUCT                                                              LOCATION                                       REASON
included     controller   tutorial.kubebuilder.io/project/internal/controller.CronJobReconciler  internal/controller/cronjob_controller.go:49
unsupported  call         log := logf.FromContext(ctx)                                           internal/controller/cronjob_controller.go:101  unsummarized call to sigs.k8s.io/controller-runtime/pkg/log.FromContext; ...
...
unsupported  preamble     if r.Clock == nil { ...                                                internal/controller/cronjob_controller.go:553  expected an unstructured object declaration
unresolved   field        cronJob.spec.concurrencyPolicy                                         internal/controller/cronjob_controller.go:444  no abstraction selects this field
inventory mode: 55 unsupported or unresolved items in the selected packages; --strict fails on them
```

Read the two lists that matter for adoption:

- `unsupported`: constructs arch cannot read, each with its source text, a
  reason and `file:line`. The list has every unsupported construct of
  `Reconcile`, and the first one of `SetupWithManager`. You cannot change
  upstream source, so each row is either an extractor gap to report or a
  reason to describe the controller with a boundary model (section 7).
- `unresolved`: object fields that `Reconcile` reads or writes but no
  abstraction selects. An abstraction document resolves them.

`testdata/external/cronjob/FOLLOWUPS.md` groups the CronJob gaps and ranks
the extensions that would close them.

## 5. Strict mode

`--strict` turns the report into a failure:

```sh
arch --strict support
```

```text
arch support: verification mode (--strict) refuses incomplete semantics: 55 unsupported or unresolved items in the selected packages, first call log := logf.FromContext(ctx) at internal/controller/cronjob_controller.go:101
```

## 6. What fails closed

`testdata/external/cronjob` also has an abstraction document and a
controller declaration in its `project.yaml`. With them in place, the
fields they select are read further, and extraction still fails:

```sh
arch --strict extract
```

```text
arch extract: unsupported constructs: 55 found, listed in generated/support.yaml
```

Without an IR, the later commands refuse to run:

```sh
arch generate
arch check
arch replay
arch render
```

```text
arch generate: generated/ir.yaml not found; run arch extract first
arch check: generated/ir.yaml not found; run arch extract and arch generate first
arch replay: no traces match generated/traces/*.itf.json; run arch check first or pass --traces
arch render: no workspace in generated/likec4; run arch generate first
```

`testdata/external/cronjob/report.yaml` records this run as an `incomplete`
`VerificationReport` with only unsupported findings.

## 7. Write a boundary model when extraction is not possible

A `BoundaryModel` describes an unmodified upstream controller
([pkg/boundary](../pkg/boundary)). It declares:

- `upstream` and `version`: the pinned module and release. `digest` is
  optional.
- `model`: a `TransitionModel` file with the controller's bounded
  behaviour. A boundary without a model describes but does not compose.
- `owns`, `watches` and `effects`: the resources it owns, the resources it
  watches, and its API operations with consistency and options.
- `ports`: the state it exposes to other components. A port names a
  resource, or an authored contract and version, and a direction.
- `assumptions`: each statement has an `evidence` of `declared` (from
  upstream documentation, with a `reference`) or `assumed`, and a
  `conformance` hook (`documentation` with a reference, or `none`).

Every fact of a boundary enters the IR as `assumed`. A passing run never
upgrades it to `extracted`.

The library ships boundary models for these upstream controllers:

| Document | Upstream |
| --- | --- |
| `pkg/boundary/library/flux/kustomization.yaml` | kustomize-controller v1.4.0 |
| `pkg/boundary/library/flux/gitrepository.yaml` | source-controller v1.4.1 |
| `pkg/boundary/library/flux/helmrelease.yaml` | helm-controller v1.1.0 |
| `pkg/boundary/library/clusterapi/clustercache.yaml` | Cluster API v1.9.0 multi-cluster cache |

An excerpt of the Kustomization boundary:

```yaml
apiVersion: architecture.local/v1alpha1
kind: BoundaryModel
metadata:
  name: flux-kustomize-controller
spec:
  upstream: github.com/fluxcd/kustomize-controller
  version: v1.4.0
  model: kustomization-model.yaml
  owns:
  - {group: kustomize.toolkit.fluxcd.io, version: v1, kind: Kustomization}
  ports:
    applied:
      state: applied
      contract: kustomize.toolkit.fluxcd.io/applied
      version: v1
      direction: out
    ready:
      state: ready
      resource: {group: kustomize.toolkit.fluxcd.io, version: v1, kind: Kustomization}
      direction: out
  assumptions:
  - id: dependsOn
    statement: The controller does not apply a Kustomization until every Kustomization in spec.dependsOn is ready.
    evidence: declared
    reference: https://fluxcd.io/flux/components/kustomize/kustomizations/#dependencies
    conformance: {kind: documentation, reference: https://fluxcd.io/flux/components/kustomize/kustomizations/#dependencies}
```

Copy the library documents you use into your repository, next to their
model files, so that they are pinned with it. Load one from Go with
`boundary.Load`. It checks the spec, the evidence of each assumption,
that the model compiles and that each port names a model state.

## 8. Check provider types against contracts

An `InteractionContract` declares the fields a provider type must have,
with their JSON types. The Cluster API provider contracts ship under
`pkg/contracts/library/clusterapi`:

| Document | Contract |
| --- | --- |
| `infrastructure-machine-v1beta1.yaml` | `capi.infrastructure-machine/v1beta1` |
| `infrastructure-machine-v1beta2.yaml` | `capi.infrastructure-machine/v1beta2` |
| `infrastructure-cluster-v1beta1.yaml` | `capi.infrastructure-cluster/v1beta1` |
| `bootstrap-config-v1beta1.yaml` | `capi.bootstrap-config/v1beta1` |

An upstream type needs no marker. Name it with an external selector, and
`contracts.Inspect` resolves it from the module cache:

```go
def, err := contracts.Load("infrastructure-machine-v1beta2.yaml")
evidence, err := contracts.Inspect(root, []contracts.Definition{def}, nil,
	[]contracts.Implementation{{
		Contract: "capi.infrastructure-machine/v1beta2",
		Package:  "example.org/capiprovider",
		Type:     "InfraMachineV1Beta2",
	}})
```

`root` is a module that requires the provider module. The evidence maps
each contract field to the type's JSON path and records the module version
and source hashes. A missing field or a wrong type fails with the field
name. A check establishes structural compatibility only. List, map and
condition fields are not checkable yet.

## 9. Compose with owned controllers

A `SystemComposition` joins boundary components and owned models through
their ports. arch has no command for it yet; call `composition.Load` from
Go. This system composes the Flux Kustomization boundary with an owned
controller that acts on the applied objects once the Kustomization is
ready:

```yaml
# file: system.yaml
apiVersion: architecture.local/v1alpha1
kind: SystemComposition
metadata:
  name: system
spec:
  name: FluxSystem
  autoLink: true
  components:
    - name: flux
      boundary: boundaries/flux/kustomization.yaml
    - name: consumer
      model: consumer.yaml
      ports:
        manifests:
          state: manifests
          contract: kustomize.toolkit.fluxcd.io/applied
          version: v1
          direction: in
        kustomization:
          state: kustomizationReady
          contract: resource:kustomize.toolkit.fluxcd.io/v1/Kustomization
          version: v1
          direction: in
  links: []
  properties:
    - name: reconcileAfterApply
      expression: '!consumer_reconciled || flux_applied'
```

The owned component is a `TransitionModel`. Here it is written by hand.
For an extracted controller, use the `model.yaml` that `arch generate`
writes:

```yaml
# file: consumer.yaml
apiVersion: architecture.local/v1alpha1
kind: TransitionModel
metadata:
  name: consumer
spec:
  name: Consumer
  state:
    manifests: {type: bool, initial: 'false'}
    kustomizationReady: {type: bool, initial: 'false'}
    reconciled: {type: bool, initial: 'false'}
  actions:
    - name: reconcile
      guard: manifests && kustomizationReady && !reconciled
      updates: {reconciled: 'true'}
  properties: []
```

Load it from the directory that holds `system.yaml`, `consumer.yaml` and
`boundaries/flux`:

```go
loaded, err := composition.Load(".", "system.yaml")
```

`autoLink: true` binds each input port to the one output with the same
contract and version. A resource port's contract is
`resource:<group>/<version>/<kind>` with version `v1`. Two providers of one
contract fail until a `links` entry picks one. The boundary component has
evidence `assumed`. In the composed model the consumer's `reconcile` stays
disabled until Flux has applied and marked the Kustomization ready.

A binding establishes connectivity only. Protocol conformance is a
separate check (`ProtocolContract`, `composition.ComposeWithProtocols`).
Clusters stay apart: a join never crosses clusters without an explicit
link.

## 10. Run it in CI

The reusable workflow (`.github/workflows/arch.yml`) stops at its Extract
step while any construct is unsupported, in both modes. For an upstream
controller that does not extract, run `arch support` in a job of your own.
It exits zero in inventory mode and prints the table above. Run the
boundary and contract checks as Go tests, as `pkg/boundary` and
`pkg/contracts` do.

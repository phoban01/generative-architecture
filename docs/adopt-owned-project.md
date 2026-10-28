# Adopting arch in a project you own

This guide is for a controller-runtime project whose source you control.
You can change the source to fit the supported subset. For an upstream
controller that you must not change, read
[adopt-upstream-project.md](adopt-upstream-project.md).

The example is the reference system in `examples/refsystem`
([ADR-0004](adr/0004-reference-system.md)). It has three controllers:
tenant, database and backup. Today arch extracts all three with no
unsupported construct. The reference system has no abstraction document
yet, and `arch generate` takes one controller per project. So sections 7 to
9 use the ConfigMirror test project, `cmd/arch/testdata/configmirror`,
which runs the whole pipeline.

`TestOwnedProjectGuideWalk` in `cmd/arch/guide_test.go` runs every command
in this guide and checks the messages it quotes. The commands run from the
repository root unless a section says otherwise.

## 1. What to pin

- **Go.** `go.mod` declares Go 1.25.0 as the minimum. The README lists the
  supported releases.
- **arch.** No release is tagged yet. Install a commit of this module:

  ```sh
  go install github.com/phoban01/generative-architecture/cmd/arch@<commit>
  ```

  In a checkout of this repository, `make build` writes `bin/arch`.
- **Quint 0.33.x and LikeC4 1.59.x.** `arch check` and `arch render` call
  them as executables. Set `QUINT_BIN` and `LIKEC4_BIN`, or put `quint`
  and `likec4` on `PATH`. Pin them in a `package.json` with a lockfile, as
  `hack/tools` does.
- **Your module.** arch type-checks your packages with your own `go.mod`.
  The module must build, and its dependencies must be in the module cache.

## 2. Write the project document

Run `arch init` in the directory that will hold the project document. Name
the module and the packages that hold the API types and the controllers:

```sh
arch init --module github.com/phoban01/generative-architecture \
  --package ./examples/refsystem/api/... \
  --package ./examples/refsystem/controllers/tenant \
  --package ./examples/refsystem/controllers/database \
  --package ./examples/refsystem/controllers/backup \
  --name refsystem
```

It writes `project.yaml` and an empty `constraints.yaml`. It refuses to
replace them unless you pass `--force`. Paths in `project.yaml` are relative
to the document. Package patterns are relative to the module root. arch
finds the module root by walking up from the document to the `go.mod` that
declares the module.

`arch init` writes no catalog, abstraction or controller declarations.
The next sections add them by hand. `examples/refsystem/project.yaml` is
the result for the reference system.

## 3. Add a catalog

arch maps each Go API type to a group, version and kind through the
catalog. List your CRD manifests, and a discovery snapshot for the built-in
kinds your controllers use:

```yaml
spec:
  catalog:
    crds:
      - config/crd/refsystem.example.org_tenants.yaml
      - config/crd/refsystem.example.org_databases.yaml
      - config/crd/refsystem.example.org_backups.yaml
    discovery:
      - config/discovery/builtin.json
```

A discovery snapshot is a JSON list of `APIResourceList` documents. Save
the output of `kubectl get --raw /apis/<group>/<version>` (or `/api/v1`)
and wrap it in `[ ]`. Keep only the resources you use.
`examples/refsystem/config/discovery/builtin.json` lists Namespace,
ResourceQuota, Secret and Job.

A Go type matches a catalog entry by kind and by the name of its Go
package, which must equal the version. Two entries with the same kind and
version make the match ambiguous. Without a match, arch reports the watch
as an unknown resource with its location.

## 4. Extract and read the support report

```sh
arch --project examples/refsystem/project.yaml --out examples/refsystem/generated extract
```

`arch extract` discovers the reconcilers in the selected packages by type
information. It needs no marker. It reads each `SetupWithManager` and each
`Reconcile`, and writes `support.yaml` and `ir.yaml` to the `--out`
directory. It writes `ir.yaml` only when no construct is unsupported.

On the reference system it finds 3 controllers and 7 watches, and it
prints:

```text
inventory mode: 39 unsupported or unresolved items in the selected packages; --strict fails on them
```

All 39 items are unresolved fields. None is unsupported. A field is
unresolved when `Reconcile` reads or writes it but no abstraction selects
it. The reference system has no abstraction yet.

The support report has four lists:

| List | Meaning |
| --- | --- |
| `included` | Extracted constructs: controllers, watches and the `Reconcile` methods that extract. |
| `summarized` | Constructs replaced by a summary, with the reason. |
| `unsupported` | Constructs arch cannot read. Each has a kind, the source text, a reason and `file:line`. |
| `unresolved` | IR facts whose evidence is unresolved, and object fields that `Reconcile` reads or writes but no abstraction selects. |

`arch support` extracts in memory, prints the same lists as a table and
writes nothing:

```sh
arch --project examples/refsystem/project.yaml support
```

On the reference system the table starts like this (columns shortened):

```text
STATUS      KIND        CONSTRUCT                                                                                    LOCATION                                                REASON
included    controller  github.com/phoban01/generative-architecture/examples/refsystem/controllers/backup.BackupReconciler  examples/refsystem/controllers/backup/controller.go:34
included    reconcile   github.com/phoban01/generative-architecture/examples/refsystem/controllers/backup.BackupReconciler  examples/refsystem/controllers/backup/controller.go:45  39 process nodes
...
unresolved  field       backup.metadata.namespace                                                                    examples/refsystem/controllers/backup/controller.go:56  no abstraction selects this field
```

## 5. Inventory mode and strict mode

Without `--strict`, arch runs in inventory mode. It reports unsupported
constructs and unresolved facts and goes on where it can. With `--strict`,
it runs in verification mode and fails on them. Global flags come before
the command:

```sh
arch --strict --project examples/refsystem/project.yaml --out examples/refsystem/generated extract
```

```text
arch extract: verification mode (--strict) refuses incomplete semantics: 39 unsupported or unresolved items in the selected packages, first field backup.metadata.namespace at examples/refsystem/controllers/backup/controller.go:56
```

The command writes the same files and then exits non-zero. `--strict`
applies to `extract`, `generate`, `check`, `support` and `replay`. Use
inventory mode while you adopt. Use strict mode in CI once the report is
clean.

## 6. Fix what is unsupported

You own the source, so the shortest path is often to change it.
[supported-patterns.md](supported-patterns.md) lists the supported ways to
build and wire a reconciler, the builder forms and the constructs
`Reconcile` may use. The reference-system controllers stay inside that
list. `examples/refsystem/README.md` records one change made for the
extractor: the Secret has the Database's name, because the extractor cannot
yet lower a string concatenation with a field.

Add a marker only to resolve an ambiguity. `+arch:controller=<name>`
renames a controller. You need it only when two packages declare
reconcilers of the same type name. A `+kubebuilder:object:root=true` type
is a resource without an arch marker.

## 7. Write the abstraction document

The abstraction document says which objects and fields enter the model
([ADR-0005](adr/0005-explicit-abstraction.md)). Name it in `project.yaml`:

```yaml
spec:
  abstraction: abstraction.yaml
```

For each resource, declare:

- `gvk`: it must be in the catalog.
- `objects`: a fixed, small population with fixed names.
- `fields`: JSON paths of type `string`, `bool` or `int`. Path elements are
  identifiers. Only the key after `metadata.annotations` or
  `metadata.labels` may be any string, and such a field needs a `name`.
- `valueDomains`: the values each field may take, as model literals.
- `identity`: usually `[metadata, name]`.
- `references`: a field that names an object of another declared resource.

Then set `bounds` (`maxObjectsPerResource`, `maxInvocations`) and list what
the abstraction leaves out under `excludes`. Reports repeat both.

The reference system has no abstraction document yet. ConfigMirror's is
`examples/configmirror/abstraction.yaml`:

```yaml
spec:
  resources:
    - gvk: {group: "", version: v1, kind: ConfigMap}
      objects:
        - {id: source, namespace: demo, name: source}
        - {id: copy, namespace: demo, name: copy}
      fields:
        - {path: [data, value], type: string}
      valueDomains:
        data.value: ['"a"', '"b"', '""']
      identity:
        - [metadata, name]
  locals:
    - {name: snapshot, type: string}
  bounds:
    maxObjectsPerResource: 2
    maxInvocations: 3
  excludes:
    - delete
    - list
```

With an abstraction, the fields it selects are no longer unresolved.
`arch --strict` passes on ConfigMirror.

## 8. Declare the controller and write constraints

`arch generate` needs facts that neither the source nor the abstraction
carries. Declare them under `spec.controllers`, and set `runtime.maxTicks`.
`cmd/arch/testdata/configmirror/project.yaml` does this for ConfigMirror:

```yaml
spec:
  runtime:
    profile: controller-runtime
    maxTicks: 20
  controllers:
    - receiver: Reconciler
      name: mirror
      workers: 1
      retryTicks: 2
      key: source
      effects:
        - call: sigs.k8s.io/controller-runtime/pkg/client.Client.Get
          effect: readSource
        - call: sigs.k8s.io/controller-runtime/pkg/client.Reader.Get
          effect: readTarget
        - call: sigs.k8s.io/controller-runtime/pkg/client.Client.Update
          effect: writeTarget
      objectKeys:
        github.com/phoban01/generative-architecture/examples/configmirror.Reconciler.Target: copy
      locals:
        source.data.value: snapshot
  initial:
    source: '"a"'
    copy: '""'
```

- `receiver` is the Go type name that `arch extract` reports.
- `key` is the abstraction object the reconcile request addresses.
- `effects` names the effect of each client method the reconciler calls,
  as `<package path>.<interface>.<method>`.
- `objectKeys` and `locals` map Go key fields and locals to abstraction
  objects and locals.
- `initial` sets the initial value of each object's field.

Write the properties the model must keep in the `constraints.yaml` that
`arch init` created. Constraints are CEL over the generated model's names,
and extraction never rewrites them. ConfigMirror's are in
`examples/configmirror/constraints.yaml`:

```yaml
spec:
  properties:
    - name: writesObservedValue
      expression: 'event != "writeTarget_ok" || ConfigMap_copy_data_value == snapshot'
```

## 9. Generate, check, replay and render

`arch generate` refuses the reference system today:

```sh
arch --project examples/refsystem/project.yaml --out examples/refsystem/generated generate
```

```text
arch generate: unsupported operation: examples/refsystem/generated/ir.yaml has 3 controllers; composing more than one controller into one model is not supported yet
```

ConfigMirror has one controller, so the whole pipeline runs on it:

```sh
arch --project cmd/arch/testdata/configmirror/project.yaml --out /tmp/configmirror extract
arch --project cmd/arch/testdata/configmirror/project.yaml --out /tmp/configmirror generate
arch --project cmd/arch/testdata/configmirror/project.yaml --out /tmp/configmirror check
arch --project cmd/arch/testdata/configmirror/project.yaml --out /tmp/configmirror replay
arch --project cmd/arch/testdata/configmirror/project.yaml --out /tmp/configmirror render
```

- `arch generate` writes `model.yaml`, `Model.qnt`, the LikeC4 workspace
  and `receipts.yaml`.
- `arch check` samples the model with Quint, writes the traces and
  `report.yaml`, and fails on a constraint violation. It prints:

  ```text
  check passed (inventory mode): 40 traces, 2400 transitions, 0 findings
  ```

- `arch replay` replays the traces against the model and writes
  `replay.yaml`. arch has no replay adapter yet, so replay reports one
  unsupported finding in every run:

  ```text
  replay incomplete (inventory mode): 40 traces, 2400 transitions, 1 findings, 1 unsupported
  ```

- `arch render` builds the LikeC4 site into `site/` under `--out`.

`check` needs Quint and `render` needs LikeC4. The guide test runs
`check` and `replay` only when `QUINT_BIN` is set, and `render` only when
`LIKEC4_BIN` is set. Sampling is bounded. A pass is
not exhaustive verification. Each report states its scope, bounds and tool
versions.

## 10. Run it in CI

Call the reusable workflow from your repository:

```yaml
jobs:
  architecture:
    uses: phoban01/generative-architecture/.github/workflows/arch.yml@<commit>
    with:
      project: project.yaml
      arch-package: github.com/phoban01/generative-architecture/cmd/arch@<commit>
      tools-dir: hack/tools   # a package.json and lockfile that pin Quint and LikeC4
```

It runs `extract`, `generate`, `check`, `replay`, `render` and an IR diff,
and uploads the output directory. `extract`, `generate` and `check` run
in strict mode by default (`strict: true`). Replay runs in inventory mode
(`replay-strict: false`). The `architecture` job in this repository's
`ci.yml` runs the workflow on the ConfigMirror test project.

The workflow fails at its Extract step in strict mode while any item is
unsupported or unresolved. On the reference system that is today. Until
the report is clean, run `arch support` in a job of your own. It exits zero
in inventory mode and prints the table above.

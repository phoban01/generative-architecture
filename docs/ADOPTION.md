# Adopting arch in a controller-runtime project

Status: draft (2026-10-27). This guide was walked step by step on the
kubebuilder CronJob tutorial controller pinned under
`testdata/external/cronjob` (Checkpoint B). Every command below ran on that
controller, and the output shown is what it printed. A test in
`cmd/arch/external_test.go` repeats each command. [ADR-0008](adr/0008-checkpoint-b-findings.md)
records what the walk found. The final guides come with v0.1.0 (#110).

Read this first: on an unmodified kubebuilder controller, `arch extract`
fails closed today. The guide shows how to get to that point, how to read
the result, and what each later step needs once extraction passes.

## 1. What to pin

- **Go.** The two most recent minor releases (1.26 and 1.27).
- **arch.** No release is tagged yet. Pin a commit of this module:

  ```sh
  go install github.com/phoban01/generative-architecture/cmd/arch@<commit>
  ```

  The reusable workflow runs the same command (section 9).
- **Quint 0.33.x and LikeC4 1.59.x.** `arch check` and `arch render` call
  them as executables. Set `QUINT_BIN` and `LIKEC4_BIN`, or pass `--quint`
  and `--likec4`. Pin them in a `package.json` with a lockfile, as
  `hack/tools` does.
- **Your module.** arch type-checks your packages with your own `go.mod`.
  The module must build, and its dependencies must be in the module cache.
  Check it like this:

  ```sh
  GOWORK=off go build ./...
  ```

  The CronJob pin moved controller-runtime from v0.22.1 to v0.23.3 so that
  the tests in this repository build from the local cache. The source
  built unchanged. arch itself matches client calls by package path, not
  by version.

## 2. Write the project document

Run `arch init` in the directory that will hold the project document. Name
the controller package and the API package:

```sh
arch init --module tutorial.kubebuilder.io/project \
  --package ./internal/controller --package ./api/v1 --name cronjob
```

It writes `project.yaml` and an empty `constraints.yaml`. It refuses to
replace them unless you pass `--force`. Paths in `project.yaml` are relative
to the document. Package patterns are relative to the module root.

`arch init` does not write a catalog, an abstraction or controller
declarations. Sections 3, 6 and 7 add them by hand.

## 3. Add a catalog

arch maps each Go API type to a group, version and kind through the
catalog. List your CRD manifests and a discovery snapshot for built-in
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
the output of `kubectl get --raw /apis/<group>/<version>` (or `/api/v1`) and
wrap it in `[ ]`.

A Go type matches a catalog entry by kind name and by the name of its Go
package, which must equal the version. Two entries with the same kind and
version make the match ambiguous. The CronJob API package is named `v1`,
and so is `k8s.io/api/batch/v1`. The full `batch/v1` snapshot also lists a
`CronJob`, so the CronJob snapshot keeps only the `Job` resource. Without a
match, a watch is reported as an unknown resource with its location.

A missing catalog does not always show at once. On the CronJob controller,
`arch extract` stops at the setup preamble before it reads any watch, so
the catalog changes no watch in the report.

## 4. Extract and read the support report

```sh
arch extract
```

`arch extract` discovers the reconcilers in the selected packages and reads
each `SetupWithManager` and each `Reconcile`. It needs no abstraction to
read `Reconcile` (ADR-0008). It writes `generated/support.yaml` and, when
nothing is unsupported, `generated/ir.yaml`. Use `--out` for another
directory.

On the CronJob controller it prints:

```text
arch extract: unsupported constructs: 54 found, listed in generated/support.yaml
```

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
arch support
```

On the CronJob controller the table has 54 unsupported rows and one
unresolved row. It starts and ends like this:

```text
STATUS       KIND         CONSTRUCT                                                              LOCATION                                       REASON
included     controller   tutorial.kubebuilder.io/project/internal/controller.CronJobReconciler  internal/controller/cronjob_controller.go:49
unsupported  call         log := logf.FromContext(ctx)                                           internal/controller/cronjob_controller.go:101  unsummarized call to sigs.k8s.io/controller-runtime/pkg/log.FromContext; ...
...
unsupported  preamble     if r.Clock == nil { ...                                                internal/controller/cronjob_controller.go:553  expected an unstructured object declaration
unresolved   field        cronJob.spec.concurrencyPolicy                                         internal/controller/cronjob_controller.go:444  no abstraction selects this field
inventory mode: 55 unsupported or unresolved items in the selected packages; --strict fails on them
```

The report lists every unsupported construct of `Reconcile`, and the first
one of `SetupWithManager`. `testdata/external/cronjob/FOLLOWUPS.md` groups
the gaps. [docs/supported-patterns.md](supported-patterns.md) lists the
supported ways to build and wire a reconciler and the constructs
`Reconcile` may use.

## 5. Inventory mode and strict mode

Without `--strict`, arch runs in inventory mode. It reports unsupported
constructs and unresolved facts and goes on where it can. With `--strict`,
it runs in verification mode and fails on them:

```sh
arch --strict support
```

```text
arch support: verification mode (--strict) refuses incomplete semantics: 55 unsupported or unresolved items in the selected packages, first call log := logf.FromContext(ctx) at internal/controller/cronjob_controller.go:101
```

`--strict` applies to `extract`, `generate`, `check`, `support` and
`replay`. Use inventory mode while you adopt. Use strict mode in CI once the
report is clean.

## 6. What fails closed

arch never guesses. These stop the pipeline with a message:

- An unsupported construct. `arch extract` exits non-zero in both modes,
  writes only `support.yaml` and removes an old `ir.yaml`.
- No IR. `arch generate` and `arch check` refuse to run:

  ```text
  arch generate: generated/ir.yaml not found; run arch extract first
  arch check: generated/ir.yaml not found; run arch extract and arch generate first
  ```

- No traces. `arch replay` needs the traces `arch check` writes:

  ```text
  arch replay: no traces match generated/traces/*.itf.json; run arch check first or pass --traces
  ```

- No workspace. `arch render` needs the LikeC4 workspace `arch generate`
  writes.
- A discovered controller without a declaration, more than one controller
  in a project, or a client method other than `Get` and `Update`. `arch
  generate` names each one.
- A reconciler built in an unsupported way. See
  [docs/supported-patterns.md](supported-patterns.md).

## 7. Write the abstraction document

The abstraction document says which objects and fields enter the model
([ADR-0005](adr/0005-explicit-abstraction.md)). Name it in `project.yaml`:

```yaml
spec:
  abstraction: abstraction.yaml
```

For each resource, declare:

- `gvk`: it must be in the catalog.
- `objects`: a fixed, small population with fixed names. The CronJob
  controller names each Job `<cronjob>-<unix time>`, so the CronJob
  abstraction declares the Jobs of two scheduled runs.
- `fields`: JSON paths of type `string`, `bool` or `int`. Path elements are
  identifiers. Only the key after `metadata.annotations` or
  `metadata.labels` may be any string, and such a field needs a `name`.
- `valueDomains`: the values each field may take, as model literals.
- `identity`: usually `[metadata, name]`.
- `references`: a field that names an object of another declared resource.

Then set `bounds` (`maxObjectsPerResource`, `maxInvocations`) and list what
the abstraction leaves out under `excludes`. Reports repeat both.

`testdata/external/cronjob/abstraction.yaml` is the CronJob example. It
shows three limits an adopter meets today:

- There is no list, pointer or time type. `spec.suspend` (a `*bool`) is a
  `bool`. `status.lastScheduleTime` is a `string` with three instants.
  `status.active` is left out; the model sees the Jobs instead.
- An owner reference has no form. `ownerReferences` is a list, but the
  loader accepts `[metadata, ownerReferences, name]` as a scalar path.
- `abstraction.Load` accepts several fields per resource, but `arch
  generate` takes exactly one.

## 8. Declare the controller

`arch generate` needs facts that neither the source nor the abstraction
carries. Declare them under `spec.controllers` and set `runtime.maxTicks`:

```yaml
spec:
  runtime:
    profile: controller-runtime
    maxTicks: 20
  controllers:
    - receiver: CronJobReconciler
      name: cronjob
      workers: 1
      retryTicks: 2
      key: cron
      effects:
        - call: sigs.k8s.io/controller-runtime/pkg/client.Reader.Get
          effect: readCronJob
        - call: sigs.k8s.io/controller-runtime/pkg/client.SubResourceWriter.Update
          effect: updateStatus
```

- `receiver` is the Go type name that `arch extract` reports.
- `key` is the abstraction object the reconcile request addresses.
- `effects` names the effect of each client method the reconciler calls,
  as `<package path>.<interface>.<method>`. Name the interface that
  declares the method, such as `client.Writer` for `Create`.
- `objectKeys` and `locals` map Go key fields and locals to abstraction
  objects and locals, when the reconciler needs them.
- `initial` sets the initial value of each object's field.

## 9. Generate, check and replay

Once `arch extract` writes an IR, run:

```sh
arch generate
arch check
arch replay
arch render
```

- `arch generate` writes `model.yaml`, `Model.qnt`, the LikeC4 workspace and
  `receipts.yaml`.
- `arch check` samples the model with Quint, writes the traces and
  `report.yaml`, and fails on a constraint violation.
- `arch replay` replays the traces against the model and writes
  `replay.yaml`. arch has no replay adapter yet, so replay reports one
  unsupported finding in every run.
- `arch render` builds the LikeC4 site.

Sampling is bounded. A pass is not exhaustive verification. Each report
states its scope, bounds and tool versions.

On the CronJob controller all four refuse to run (section 6).
`testdata/external/cronjob/report.yaml` records the run as an `incomplete`
`VerificationReport` with only unsupported findings.

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
(`replay-strict: false`).

The workflow stops at its Extract step while the support report lists an
unsupported construct, in both modes. Until extraction is clean, run
`arch support` in a job of your own. It exits zero in inventory mode and
prints the table above.

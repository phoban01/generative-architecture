# v0.1.0 release checklist

Status: 2026-10-28. v0.1.0 is Checkpoint C in
[PROJECT-PLAN.md](../PROJECT-PLAN.md). The owner creates the tag. This
document lists the steps before it and the state of each one.

`TestReleaseChecklistReferences` in `cmd/arch/guide_test.go` checks that
every file and `make` target this checklist names exists.

## State

| Step | State |
| --- | --- |
| Module path | Done (#109) |
| CI green on GitHub | Done: run 40762032430 on 78b4f0e, all jobs passed |
| Evidence refreshed with the pinned tools | Done (cc419ec) |
| Envtest job | Done: passed on Go 1.26.x and 1.27.x |
| Architecture pipeline job | Done: passed on 78b4f0e |
| Compatibility policy for `pkg/` | Done: written below |
| Tool version ranges | Done (ADR-0003) |
| Go versions | Done: 1.26 and 1.27 |
| Receipt drift scope | Done (#142, ADR-0009) |
| Stray `devbox.lock` change | Done: restored |
| Adoption guides | Done (#110) |
| Tag and release notes | Tag v0.1.0 pushed on 78b4f0e; the GitHub release is the owner's act |

## 1. Module path

Done (#109). The module path is `github.com/phoban01/generative-architecture`.
`go.mod`, every import and the committed evidence use it. `LIBRARY.md`
shows the `go get` line.

## 2. CI green on GitHub

Not verifiable from a local checkout. Push `main` and open the CI run of
the release commit. Check that these jobs of `.github/workflows/ci.yml`
pass:

- `go`: build, vet, test and the consumer module.
- `envtest`: once for each Go release in the matrix.
- `architecture`: the reusable workflow `.github/workflows/arch.yml` on the
  ConfigMirror project.

Run 40762032430 on 78b4f0e passed all four jobs on 2026-10-28. The first
run on cc419ec failed because the runner had an empty module cache for the
pinned CronJob module; the `go` job now fills it with `go mod download`.

## 3. Refresh all evidence with the pinned tools

Run on the release commit, with Quint 0.33.x and LikeC4 1.59.x from
`hack/tools` (`devbox shell` sets `QUINT_BIN` and `LIKEC4_BIN`):

```sh
GOFLAGS=-p=2 make verify
```

`make verify` resamples the sample controller, the runtime group and
ConfigMirror, runs every test and checks the consumer module.
`GOFLAGS=-p=2` keeps the test run within the memory of a small machine.
Commit any refreshed file.

## 4. Envtest job

The `envtest` job runs `make envtest` after the build job, once per
supported Go release. It installs kube-apiserver and etcd with
setup-envtest and fails when the assets cannot be installed, a test skips
or no test runs. `hack/ci` checks the job's shape in `go test`. Locally:

```sh
make envtest
```

## 5. Architecture pipeline job

The `architecture` job calls `.github/workflows/arch.yml` on
`cmd/arch/testdata/configmirror/project.yaml` with tools from `hack/tools`.
It runs `extract`, `generate`, `check`, `replay`, `render` and `diff`.
Locally:

```sh
make arch-pipeline
```

## 6. Compatibility policy

v0.1.0 is a first release, not a stable API. The module follows semantic
versioning for v0.

What v0.1.x patch releases keep:

- The module path.
- The `arch` command names and the global flags `--project`, `--out` and
  `--strict`.
- The document kinds and fields that v0.1.0 reads under
  `architecture.local/v1alpha1`.
- The IR schema version `v1alpha1` (`pkg/ir`).

A patch release fixes defects. It does not remove or rename any of these.

What may change in a minor release before v1:

- Any exported identifier under `pkg/`. The packages are experimental.
- Document kinds and fields. A change that breaks a document says so in
  the release notes.
- The IR schema, with a new schema version.
- Generated file formats, diagnostic text and report layout.
- The supported Go releases and tool version ranges.

## 7. Tool version ranges

Done (ADR-0003, `docs/adr/0003-external-tools-by-version-range.md`).
`pkg/quint` accepts Quint in [0.33.0, 0.34.0). `pkg/likec4` accepts LikeC4
in [1.59.0, 1.60.0). Each receipt records the exact version that ran.
`hack/tools/package.json` pins the versions CI uses.

## 8. Go versions

Done. The supported releases are the two most recent Go minors, 1.26 and
1.27, as `README.md` states. `go.mod` declares `go 1.26.0`, and so do the
consumer module under `testdata/consumer` and the pinned CronJob module
under `testdata/external/cronjob`. The `go` job installs the version in
`go.mod`, and the `envtest` matrix runs 1.26.x and 1.27.x. Go 1.25 is not
supported.

## 9. Receipt drift scope

Done: #142, ADR-0009. Receipts hash the full in-module closure and list
the owned inputs: the selected packages and the extra documents. Owned
drift fails; library drift warns and is recorded in the report. `make
verify` refreshes every receipt before the tag, so no drift remains.

## 10. Stray devbox.lock change

Done. The uncommitted change set one plugin's `plugin_version` to `0.0.2`.
The committed value `0.0.5` is restored, and the tree is clean.

## 11. Tag and release

Steps 2 and 3 are done. The annotated tag `v0.1.0` points at 78b4f0e, the
commit CI passed, and is pushed. ADOPT-6 is checked: `go get` and
`go install` of v0.1.0 work from a module outside this repository.

The release itself is the owner's act: create the GitHub release from the
tag with the notes in section 12.

`LIBRARY.md`, `README.md` and `docs/adopt-owned-project.md` already name
`@v0.1.0`. ADOPT-6 and the Checkpoint C row of `PROJECT-PLAN.md` are marked
done.

## 12. Release notes outline

**Summary.** A Go library and the `arch` CLI that extract controller
behaviour, watches and client effects from controller-runtime source,
generate Quint models and LikeC4 diagrams, check authored constraints and
replay sampled traces. Sampling is bounded; a pass is not exhaustive
verification.

**Milestones completed:**

- M0 Foundations: public repository, CI, the `arch` namespace, toolchain
  ranges, ADRs.
- M1 Shared IR and catalog: `pkg/ir`, `pkg/catalog`, `pkg/abstraction`.
- M2 Typed frontends and summaries: type-resolved control flow, effects and
  watches; the summary library.
- M3 Watch and runtime composition: event-to-key graph from IR, priority
  queue, startup and deletion.
- M4 System composition and boundaries: derived ports and joins,
  `ProtocolContract`, `BoundaryModel`, Cluster API contracts, Flux
  boundaries.
- M5 Generated adapters and evidence: `adaptergen`, envtest, report
  categories, the mutation registry.
- M6 Visualization: one LikeC4 workspace with five of the six views.
- M7 CLI, CI and release: nine subcommands, `arch diff`, strict mode, the
  reusable workflow, the module path and the adoption guides.

**Checkpoints:**

- A, first complete flow: passed. ConfigMirror's model, projection,
  environment and boundary gating are generated, and its handwritten model
  is deleted. Replay detects the `wrong-write` and `direct-source`
  mutations. The receipt lists bounds and exact tool versions (ADR-0007).
- B, external adoption: reported as unsupported. The kubebuilder CronJob
  controller has a committed support report, abstraction and an
  `incomplete` VerificationReport. No model is generated, because
  extraction fails closed (ADR-0008).
- C, release: this tag.

**Known limits:** `arch generate` takes one controller per project, so
`arch` does not model or compose the reference system. `arch replay` has no
generated adapter. Extraction fails closed on common kubebuilder
constructs (`testdata/external/cronjob/FOLLOWUPS.md`). The requirements
index in `PROJECT-PLAN.md` lists every partial requirement.

**Supported toolchain:** Go per step 8, Quint 0.33.x, LikeC4 1.59.x.

# Generative architecture

A Go library and toolchain that generates architecture artifacts for a system powered by Kubernetes: **Go source + declared semantics + KRM YAML/CEL → Quint models, LikeC4 diagrams and model-based tests**, with controller-runtime and dynamic-client integration. Many controller-runtime projects compose into one system; this toolset captures that architecture from the code as models, lets you write invariants against those models, and verifies contracts and constraints with evidence.

## Start with the reference system

The reference system in [`examples/refsystem`](examples/refsystem/README.md) has three controllers: tenant, database and backup. They interact only through Kubernetes resources. Extract it from the repository root:

```sh
go run ./cmd/arch --strict --project examples/refsystem/project.yaml --out examples/refsystem/generated extract
```

arch finds the three controllers and their seven watches with no marker. It reads each `Reconcile` with no unsupported construct. The command then fails in strict mode, because the reference system has no abstraction document yet: 39 fields that the controllers read or write are unresolved. Without `--strict` the same command succeeds and writes the same files. `arch generate` does not run on the reference system yet, because it takes one controller per project.

To adopt arch, follow one of the two guides. Every command in them runs in `go test`.

- [Adopting arch in a project you own](docs/adopt-owned-project.md): init, catalog, extract, support, abstraction, generate, check, replay, render and CI.
- [Adopting arch for an upstream controller](docs/adopt-upstream-project.md): pinning, the support report, boundary models, provider contracts and composition.

[START-HERE.md](START-HERE.md) walks a sample controller from source to model without Quint or a cluster:

```sh
go run ./cmd/walkthrough          # no Kubernetes or Quint required
```

The goal is **implementation → generated system model → independent constraints → counterexamples and Go replay**. Public packages in `pkg/` support restricted source extraction, structural contract declarations, process/model composition and controller-runtime scheduling. See [Using the library](LIBRARY.md) for APIs and limits. Automatic discovery of arbitrary controller wiring remains unfinished.

See [the project plan](PROJECT-PLAN.md) for the requirements index, milestones and decision records. [docs/release.md](docs/release.md) is the v0.1.0 checklist and the compatibility policy.

## Examples

| Example | Role |
| --- | --- |
| `examples/refsystem/` | The reference system and the adoption walkthrough. Three controllers extract with no unsupported construct; the abstraction, the composed model and the Flux boundary come later. |
| `cmd/arch/testdata/configmirror/` | The ConfigMirror project document: the one project that runs the whole `arch` pipeline, in the adoption guide and in CI's `architecture` job. |
| `examples/configmirror/` | Regression fixture. The first source-generated controller: its model, replay projection, environment and boundary gating are generated; Quint traces are replayed against the real controller, a mutation suite checks detection and a receipt records the inputs. |
| `examples/database/` | Regression fixture and the source of [START-HERE.md](START-HERE.md). Process, watches and dynamic-client accesses are extracted from Go; replay runs the real reconcile against a fake dynamic API through a projection generated from `abstraction.yaml`. |
| `examples/runtimegroup/` | Regression fixture. A multi-controller runtime profile checked against real controller-runtime workers and client-go FIFO queues. |
| `testdata/external/cronjob/` | The pinned kubebuilder CronJob controller (Checkpoint B), used by the upstream adoption guide. |
| `testdata/consumer/` | A separate Go module that imports the public packages. |

## Quick start

Run from the repository root with Go 1.26 or newer on PATH:

```sh
go test ./...
go run ./cmd/walkthrough
```

These commands use the saved, hash-checked evidence and require no running cluster, Quint or Node installation. They replay existing traces; they do not produce a fresh Quint simulation. Source changes invalidate evidence receipts: refresh the affected example before expecting its replay tests to pass.

## Conformance layers

| Layer | Implemented behaviour | Where |
| --- | --- | --- |
| Structure | Go types, GVKs, fields, controller/action markers and checked semantic mappings | `pkg/sourcegen`, `pkg/contracts` |
| Process | Restricted Go AST extraction of reconcile control flow, with independent requirements and source mutations | `pkg/goflow`, `pkg/process`, `examples/database` |
| Watches and API access | Extracted builder registrations, key mappings, predicates and dynamic-client resource operations | `pkg/watchgen`, `pkg/dynamicgen` |
| Runtime | Real controller-runtime workers with client-go FIFO queues or controller-runtime priority queues; controlled cache delivery, startup sync, deletion events, retries and conflicts | `pkg/controllerruntime`, `examples/runtimegroup` |
| Composition | Processes namespaced and joined through versioned contract ports | `pkg/composition` |

Authored documents use KRM `apiVersion`, `kind`, `metadata` and `spec`; generated observations use `status`. See [KRM documents](KRM.md) and the [CEL subset](CEL.md). No architecture CRDs or live cluster are required.

## Development environment

[Devbox](https://www.jetify.com/devbox) pins the toolchain in `devbox.json` and `devbox.lock`: Go, Node.js and `gh` from nixpkgs, plus Quint and LikeC4 installed by npm from `hack/tools/package.json` and its lockfile on first shell entry. The devbox pins sit at the minimum of each supported range below; they are a convenience, not the compatibility contract. The Go module has no npm dependency; `hack/tools/` only supplies the two external CLIs.

```sh
devbox shell          # or: devbox run tools   (prints every tool version)
devbox run check      # replay hash-checked evidence
devbox run verify     # refresh evidence with the pinned Quint
devbox run render     # build the LikeC4 site
```

## Supported toolchain

Go dependencies are pinned in `go.mod` and `go.sum`. The external tools are accepted by version range, following [ADR-0003](docs/adr/0003-external-tools-by-version-range.md): the minimum version is inclusive, the next minor is the exclusive upper bound, and every verification receipt records the exact version that ran.

| Tool | Supported | Selected by |
| --- | --- | --- |
| Go | The two most recent minor releases (currently 1.26 and 1.27); `go.mod` declares the minimum language version | `devbox.json` or your own install |
| Quint | 0.33.x | `QUINT_BIN`, or `quint` on `PATH` |
| LikeC4 | 1.59.x | `LIKEC4_BIN`, or `likec4` on `PATH` |
| controller-runtime, client-go | Pinned in `go.mod` | Go modules |

The Go tools call Quint and LikeC4 as executables and never import npm code. `devbox shell` provides all of them and exports `QUINT_BIN` / `LIKEC4_BIN` (see [Development environment](#development-environment)). Outside devbox, set those variables or put the CLIs on `PATH`.

```sh
make verify
```

`make verify` refreshes the sample controller's 40 sampled Quint traces and replays them against Go, samples 40 multi-controller runtime traces, refreshes ConfigMirror's 40 traces sampled from its generated model with its mutation matrix and input-closure receipt, runs every test and checks the separate consumer module. Simulation is bounded, not exhaustive verification or a liveness proof. Fixtures also contain deliberate source mutations; a passing run means the constraints detected them.

## Continuous integration

### Envtest

Some tests start a local API server with controller-runtime's envtest, through `pkg/envtest`. They need the kube-apiserver and etcd binaries, which `setup-envtest` installs. Use the release branch of the controller-runtime version in `go.mod` and the Kubernetes minor of `k8s.io/api`:

```sh
export KUBEBUILDER_ASSETS="$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.23 use 1.35.x --bin-dir bin/envtest -p path)"
go test -p 2 -run Envtest ./...
```

Without `KUBEBUILDER_ASSETS` these tests skip. With `ENVTEST_REQUIRED=1` they fail instead, so CI cannot pass by skipping them.

`make envtest` does both steps. It reads the versions from `go.mod` and fails when the assets cannot be installed, a test skips or no test runs. The `envtest` job in `.github/workflows/ci.yml` runs it on each supported Go release after the build job. `hack/ci` parses the workflows and checks these jobs in `go test`.

### Architecture pipeline

`.github/workflows/arch.yml` is a reusable workflow for adopters. It installs `arch`, Quint and LikeC4, then runs `arch extract`, `generate`, `check`, `replay`, `render` and `diff` on one project document. It uploads the output directory as the `architecture` artifact: generated models and diagrams, the rendered site, the reports with their receipts, the sampled traces and the IR diff.

The job fails on a constraint violation or Quint finding, on a replay finding, on evidence whose inputs changed, and on any tool failure. A changed IR against the base branch is reported, not failed. Replay runs in inventory mode by default, because `arch` has no replay adapter yet. Sampling is bounded, so a pass is not exhaustive verification.

```yaml
jobs:
  architecture:
    uses: phoban01/generative-architecture/.github/workflows/arch.yml@v0.1.0
    with:
      project: project.yaml
      arch-package: github.com/phoban01/generative-architecture/cmd/arch@v0.1.0
      tools-dir: hack/tools   # a package.json and lockfile that pin Quint and LikeC4
```

Without `tools-dir`, the workflow installs the Quint and LikeC4 versions in its `quint-version` and `likec4-version` inputs. The other inputs set the sampling bounds, the seed, steering (`steer-weight`, `steer-except`) and strict mode.

The `architecture` job in `ci.yml` runs the workflow on ConfigMirror with tools from `hack/tools`, so CI uses the versions the receipts record. `make arch-pipeline` runs the same script steps locally into a temporary directory. Set `ARCH_PIPELINE_BASE` to a branch to include the IR diff.

## Diagrams and binaries

```sh
make render           # builds site/ from examples/database/generated
make serve            # likec4 start on the same directory
```

The generated LikeC4 sources cover the extracted reconcile flow, the watch routes into the queue, the resource dependencies and the dynamic-client accesses. `make build` creates the native executables under `bin/`. Binaries and the rendered `site/` are local build outputs, excluded by `.gitignore`.

## Repository layout

| Path | Purpose |
| --- | --- |
| `pkg/`, `LIBRARY.md`, `START-HERE.md` | Public APIs, library reference and the source-to-model walkthrough of the sample controller |
| `examples/database/` | Sample controller: source, authored semantics, generated models, traces and report |
| `examples/refsystem/` | Reference system: API types, CRD manifests, three controllers and their committed extraction |
| `examples/configmirror/`, `examples/runtimegroup/`, `testdata/consumer/` | First source-generated controller (Checkpoint A), runtime profile and external-module smoke test |
| `pkg/sourcegen/`, `pkg/goflow/`, `pkg/process/`, `pkg/watchgen/`, `pkg/dynamicgen/` | Structure, behaviour, watch and API-access extraction |
| `pkg/contracts/`, `pkg/composition/`, `pkg/model/`, `pkg/expr/` | Contract checks, composition, model compilation, CEL subset |
| `pkg/effect/`, `pkg/controllerruntime/`, `pkg/conformance/`, `pkg/quint/`, `pkg/krm/` | Effect gating, runtime harness, replay, Quint driver, strict KRM loading |
| `cmd/` | `arch`, the generic CLI (subcommands land milestone by milestone), plus the `walkthrough`, `library-demo` and `general-demo` entry points |
| `docs/` | Adoption guides, supported patterns, the release checklist, decision records (`adr/`) and plan reviews (`reviews/`) |

**Generated models, trace fixtures and YAML reports are intentionally retained.** Native tests and replay commands depend on them, and their hashes detect stale evidence. ITF traces retain Quint's JSON interchange format. Use `make clean` to remove local binaries and the rendered site.

## Contributing and licence

See [CONTRIBUTING.md](CONTRIBUTING.md) for the issue conventions, the test requirement and how to refresh evidence. The project is licensed under the [Apache License 2.0](LICENSE).

## Scope

The sample is bounded to one `Database`, one `DatabaseInstance`, one endpoint value and four reconcile invocations. API errors, informer and cache behaviour, multiple keys and workers, and upstream project conformance are outside it; some are exercised by the other examples. Go markers identify owned resources, processes and structural contract implementations. Versioned contract ports support automatic unambiguous joins; YAML supplies remaining assumptions and explicit bindings. Dynamic registrations, arbitrary mappers and upstream conformance are not automatically inferred.

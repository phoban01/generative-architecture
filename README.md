# Generative architecture

A Go library and toolchain that generates architecture artifacts for a system powered by Kubernetes: **Go source + declared semantics + KRM YAML/CEL → Quint models, LikeC4 diagrams and model-based tests**, with controller-runtime and dynamic-client integration. Many controller-runtime projects compose into one system; this toolset captures that architecture from the code as models, lets you write invariants against those models, and verifies contracts and constraints with evidence.

Start with [the source-to-model walkthrough](START-HERE.md). It follows a small sample controller: a `Database` resource appears, the controller checks that the provisioning contract is supported, creates or reuses a `DatabaseInstance`, waits for a separate subsystem to provision it, then publishes the instance endpoint into the `Database` status. The controller's behaviour model is generated from its Go source and checked against independently written constraints.

```sh
go run ./cmd/walkthrough          # no Kubernetes or Quint required
```

The goal is **implementation → generated system model → independent constraints → counterexamples and Go replay**. Public packages in `pkg/` support restricted source extraction, structural contract declarations, process/model composition and controller-runtime scheduling. See [Using the library](LIBRARY.md) for APIs and limits. The sample extracts supported `For`/`Watches` registrations, reference-field key mappings and common predicates from Go setup, extracts bounded dynamic-client resource accesses, and feeds event delivery into a native controller-runtime queue harness. Automatic discovery of arbitrary controller wiring remains unfinished.

See [the project plan](PROJECT-PLAN.md) for the requirements index, milestones and decision records.

## Examples

| Example | Role |
| --- | --- |
| `examples/database/` | The sample controller. Process, watches and dynamic-client accesses are extracted from Go; constraints are authored separately; replay runs the real reconcile against a fake dynamic API through a projection generated from `abstraction.yaml`. |
| `examples/refsystem/` | Neutral multi-controller reference system: API types and CRDs now, controllers and composition in later milestones |
| `examples/configmirror/` | The first source-generated controller. Typed extraction of its reconciler, an abstraction document and the built-in client summaries generate its model and its replay projection, environment and boundary gating. Quint traces of that model are replayed against the real controller, a mutation suite checks detection and a receipt records the inputs. |
| `examples/runtimegroup/` | A multi-controller runtime profile checked against real controller-runtime workers and client-go FIFO queues. |
| `testdata/consumer/` | A separate Go module that imports the public packages. |

## Quick start

Run from the repository root with Go 1.25 or newer on PATH:

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
| Quint | 0.33.x | `QUINT_BIN` or `--quint` |
| LikeC4 | 1.59.x | `LIKEC4_BIN` or `--likec4` |
| controller-runtime, client-go | Pinned in `go.mod` | Go modules |

The Go tools call Quint and LikeC4 as executables and never import npm code. `devbox shell` provides all of them and exports `QUINT_BIN` / `LIKEC4_BIN` (see [Development environment](#development-environment)). Outside devbox, set those variables or use the flags.

```sh
make verify
```

`make verify` refreshes the sample controller's 40 sampled Quint traces and replays them against Go, samples 40 multi-controller runtime traces, refreshes ConfigMirror's 40 traces sampled from its generated model with its mutation matrix and input-closure receipt, runs every test and checks the separate consumer module. Simulation is bounded, not exhaustive verification or a liveness proof. Fixtures also contain deliberate source mutations; a passing run means the constraints detected them.

## Diagrams and binaries

```sh
make render           # builds site/ from examples/database/generated
make serve            # likec4 start on the same directory
```

The generated LikeC4 sources cover the extracted reconcile flow, the watch routes into the queue, the resource dependencies and the dynamic-client accesses. `make build` creates the native executables under `bin/`. Binaries and the rendered `site/` are local build outputs, excluded by `.gitignore`.

## Repository layout

| Path | Purpose |
| --- | --- |
| `pkg/`, `LIBRARY.md`, `START-HERE.md` | Public APIs, library reference and first walkthrough |
| `examples/database/` | Sample controller: source, authored semantics, generated models, traces and report |
| `examples/refsystem/` | Reference system: API types, scheme and CRD manifests |
| `examples/configmirror/`, `examples/runtimegroup/`, `testdata/consumer/` | First source-generated controller (Checkpoint A), runtime profile and external-module smoke test |
| `pkg/sourcegen/`, `pkg/goflow/`, `pkg/process/`, `pkg/watchgen/`, `pkg/dynamicgen/` | Structure, behaviour, watch and API-access extraction |
| `pkg/contracts/`, `pkg/composition/`, `pkg/model/`, `pkg/expr/` | Contract checks, composition, model compilation, CEL subset |
| `pkg/effect/`, `pkg/controllerruntime/`, `pkg/conformance/`, `pkg/quint/`, `pkg/krm/` | Effect gating, runtime harness, replay, Quint driver, strict KRM loading |
| `cmd/` | `arch`, the generic CLI (subcommands land milestone by milestone), plus the `walkthrough`, `library-demo` and `general-demo` entry points |
| `docs/adr/`, `docs/reviews/` | Decision records and plan reviews |

**Generated models, trace fixtures and YAML reports are intentionally retained.** Native tests and replay commands depend on them, and their hashes detect stale evidence. ITF traces retain Quint's JSON interchange format. Use `make clean` to remove local binaries and the rendered site.

## Contributing and licence

See [CONTRIBUTING.md](CONTRIBUTING.md) for the issue conventions, the test requirement and how to refresh evidence. The project is licensed under the [Apache License 2.0](LICENSE).

## Scope

The sample is bounded to one `Database`, one `DatabaseInstance`, one endpoint value and four reconcile invocations. API errors, informer and cache behaviour, multiple keys and workers, and upstream project conformance are outside it; some are exercised by the other examples. Go markers identify owned resources, processes and structural contract implementations. Versioned contract ports support automatic unambiguous joins; YAML supplies remaining assumptions and explicit bindings. Dynamic registrations, arbitrary mappers and upstream conformance are not automatically inferred.

# Generative architecture

A Go library prototype for **Go source + declared semantics + KRM YAML/CEL → Quint models, LikeC4 diagrams and model-based tests**, with controller-runtime and dynamic-client integration.

Start with [the source-to-model walkthrough](START-HERE.md): the MAC reconcile flow discovers an InfraMachine, checks the assignment contract, creates/reuses a claim, waits for fulfillment, then updates InfraMachine status. Its model is generated from Go and checked against separate constraints.

```sh
go run ./cmd/walkthrough          # no Kubernetes or Quint required
```

The goal is **implementation → generated system model → independent constraints → counterexamples and Go replay**. Public packages in `pkg/` support restricted source extraction, structural contract declarations, process/model composition and controller-runtime scheduling. See [Using the library](LIBRARY.md) for APIs and limits. The MAC walkthrough now extracts supported `For`/`Watches` registrations, reference-field key mappings and common predicates from Go setup, and feeds event delivery into the native queue harness. Automatic discovery of arbitrary controller wiring remains unfinished.

The MAC walkthrough also extracts bounded dynamic-client resource accesses and replays them against an unstructured fake API. See [the project plan](PROJECT-PLAN.md) for the requirements index, milestones and decision records.

ConfigMirror is a handwritten-model integration reference, not the automatic-generation example. The larger machine-vendor example exercises the source-derived Kubernetes process and runtime layers. A separate-module test demonstrates external imports.

The root YAML files and the commands below remain the machine-vendor entrypoints.

The machine-vendor executable slice follows machine assignment:

**InfraMachine requests allocation → MAC creates MachineClaim → claim obtains a lease → MAC publishes the machine ID → infrastructure controller provisions.**

The infrastructure controller does not interpret leases. The reserve consumer is a dedicated Kubernetes cluster. These are behavioral boundaries; existing controllers need not be refactored to match the diagram.

## Quick start

Run from the repository root with Go 1.25 or newer on PATH:

```sh
go test ./...
go run ./cmd/arch check --replay
go run ./cmd/controller-demo
```

These commands use the saved, hash-checked evidence and require no running cluster, Quint or Node installation. They replay existing traces; they do not produce a fresh Quint simulation.

After editing annotated Go source, regenerate structural artifacts:

```sh
make generate
```

Source changes invalidate evidence receipts. Refresh the affected checks before expecting replay tests to pass. Contract changes additionally require review and `go run ./cmd/arch generate --update-lock`.

## Conformance layers

| Layer | Implemented behavior | Guide |
| --- | --- | --- |
| Structure | Go types, GVKs, fields, controller/action markers and checked semantic mappings | [Source code generation](SOURCE-CODEGEN.md) |
| Contract | YAML/CEL composition, Quint generation, independent Go simulator and phase-level controller replay | [Controller adapter](CONTROLLER-DEMO.md) |
| Process | Restricted Go AST extraction of MAC publication, independent requirements and source mutations | [Behavior extraction](BEHAVIOR-EXTRACTION.md) |
| Runtime | Real controller-runtime worker and client-go FIFO queue; controlled cache delivery, retries and conflicts | [Runtime conformance](RUNTIME-CONFORMANCE.md) |
| Composition | Extracted publication steps interleaved with runtime events and replayed against paused Go | [Process/runtime composition](COMPOSITION.md) |

Authored documents use KRM `apiVersion`, `kind`, `metadata` and `spec`; generated observations use `status`. See [KRM documents](KRM.md) and the [CEL subset](CEL.md). No architecture CRDs or live cluster are required.

The original MAC runtime profile selects one key, one worker and a FIFO queue. The reusable runtime also supports multiple controllers, keys and workers; `examples/runtimegroup/` exercises that profile. The composition layer retains invocation locals and permits runtime events between publication API operations. The handwritten adapters, fake API stores and environment assumptions remain visible in each layer's guide.

## Development environment

[Devbox](https://www.jetify.com/devbox) pins the toolchain in `devbox.json` and `devbox.lock`: Go, Node.js and `gh` from nixpkgs, plus Quint 0.33.0 and LikeC4 1.59.4 installed by npm from `hack/tools/package.json` and its lockfile on first shell entry. The Go module has no npm dependency; `hack/tools/` only supplies the two external CLIs.

```sh
devbox shell          # or: devbox run tools   (prints every tool version)
devbox run check      # replay hash-checked evidence
devbox run verify     # refresh evidence with the pinned Quint
devbox run render     # build the LikeC4 site
```

## Fresh verification

Dependencies are pinned in `go.mod` and `go.sum`. Fresh model checks require **Quint 0.33.0**; diagram rendering requires **LikeC4 1.59.4**. The Go tools call those external executables and never import npm code. `devbox shell` provides Go, Node, `gh` and both CLIs at the pinned versions and exports `QUINT_BIN` / `LIKEC4_BIN` (see [Development environment](#development-environment)). Outside devbox, set those variables or use the commands' `--quint` / `--likec4` flags.

```sh
go run ./cmd/arch generate
go run ./cmd/arch check
go run ./cmd/controller-demo
go run ./cmd/behavior-demo
go run ./cmd/runtime-demo
go run ./cmd/composition-demo
go test ./...
```

`arch check` generates four regression scenarios and 200 sampled traces, checks CEL/Quint agreement and replays the simulator. `controller-demo` replays those traces through MAC phase methods. `behavior-demo` extracts publication control flow and tests source mutations. `runtime-demo` generates five deterministic scenarios and 100 sampled traces, then replays the runtime adapter. `composition-demo` joins extracted process steps to that runtime and replays nine deterministic and 100 sampled traces with the Go method paused at client boundaries. `make verify` also runs the full MAC walkthrough with 40 sampled traces, samples 40 multi-controller runtime traces, refreshes ConfigMirror’s 40 sampled traces, and checks the separate consumer module. It refreshes all layers in order.

The expected demonstration results include safety counterexamples. A successful command means the expected outcomes were reproduced, not that every safety property holds. The prepared-provisioning-call race and publication read/write race remain documented in the layer guides and reports. No production MAC or upstream CAPI implementation has been verified.

## Diagrams and binaries

```sh
go run ./cmd/arch render
go run ./cmd/arch serve
```

The server shows the rendered site at `http://127.0.0.1:8767/`. Generate the runtime layer first to refresh its diagram. The site includes contract composition, source structure, the provisioning counterexample, the runtime profile and the replayed publication race.

`make build` creates the six native executables under `bin/`. Binaries and the rendered `site/` are local build outputs, excluded by `.gitignore`.

## Repository layout

| Path | Purpose |
| --- | --- |
| `pkg/`, `LIBRARY.md`, `START-HERE.md` | Public APIs, library reference and first walkthrough |
| `examples/assignment/`, `examples/runtimegroup/` | Source-derived MAC reconcile walkthrough and reusable scheduler checks |
| `examples/configmirror/`, `testdata/consumer/` | Independent controller example and external-module smoke test |
| `system.yaml` | Component selection, bindings, properties, source extraction and runtime profile reference |
| `contracts/`, `contracts.lock.yaml` | Behavioral contracts and reviewed content hashes |
| `source-mappings.yaml`, `scenarios.yaml` | Semantic Go mappings and system regression schedules |
| `behavior/`, `runtime/` | Process requirements, composition and explicit runtime configuration |
| `examples/machinevendor/api/`, `examples/machinevendor/controllerdemo/` | Illustrative API types, independently written MAC and adapter |
| `examples/machinevendor/architecture/` | CEL translation, composition, simulator, code generation and trace replay |
| `pkg/sourcegen/`, `pkg/goflow/`, `pkg/process/`, `pkg/contracts/`, `pkg/composition/` | Structural/behaviour extraction, contract checks and reusable composition |
| `examples/machinevendor/runtimecheck/`, `pkg/krm/` | Runtime conformance and strict KRM loading |
| `cmd/` | `arch`, `controller-demo`, `behavior-demo`, `runtime-demo`, `composition-demo` entry points |
| `generated/` | Generated Go dispatch, Quint, LikeC4 and KRM metadata |
| `traces/`, `reports/` | Recorded test fixtures, evidence receipts and check results |

**Generated models, trace fixtures and YAML reports are intentionally retained.** Native tests and replay commands depend on them, and their hashes detect stale evidence. The mutant Go source under `generated/behavior/` is a test artifact, not abandoned implementation code. ITF traces retain Quint's JSON interchange format.

Use `make clean` to remove local binaries, the rendered site and disposable `.txt` tool logs. It preserves everything needed for native replay. Rerun `make build` or `arch render` when those outputs are needed again.

## Scope and integration

The system model has two hosts, two InfraMachines and bounded assignment epochs. Reservations, priority preemption, diagnostics, failure replacement and cert-manager are outside this executable slice. Checks use targeted and sampled execution, not exhaustive verification or liveness proofs.

Go markers identify owned resources, processes and structural contract implementations. Versioned contract ports support automatic unambiguous joins; YAML supplies remaining assumptions and explicit bindings. The walkthrough extracts a bounded subset of controller-runtime builder wiring. Dynamic registrations, arbitrary mappers, remote repository selection and upstream conformance are not automatically inferred. See [Integration](INTEGRATION.md) for adapting this prototype to the existing system without reorganizing production code.

# ConfigMirror library example

This ordinary controller copies the source ConfigMap's `data.value` into a pre-existing target ConfigMap. It imports only the public library. It is the first source-generated controller and the Checkpoint A example. Nothing in its model is written by hand:

1. Typed extraction of `Reconcile` gives its control flow and client calls.
2. The model is generated from that program, the abstraction document and the built-in client summaries, with the controller-runtime queue, timer and clock.
3. The replay projection, environment and boundary gating are generated from the same inputs.
4. Quint samples traces of the generated model.
5. The traces are replayed against the real reconciler through a controller-runtime worker and a fake API.
6. A mutation suite checks that changed controller builds are detected.
7. A receipt records every input file's hash, the tool versions and the abstraction's bounds.

Start with `../../START-HERE.md` for the walkthrough of the workflow, shown on `../database/`.

- `controller.go`: actual reconciler.
- `project.yaml`, `abstraction.yaml`, `constraints.yaml`, `discovery.json`: the authored inputs (module and package, bounded ConfigMap abstraction with its bounds and excludes, independent properties written against the generated names, API discovery snapshot). `inputs_test.go` checks they load and agree with the controller, the adapter fixtures, the generated model and the saved traces.
- `generate.go`: derives the TransitionModel from the typed extraction of `Reconcile`, the abstraction, the built-in client summaries and the controller-runtime profile; `Load` generates and compiles it. `generate_test.go` checks the committed model is current, takes seeded walks of it under the constraints, and checks that model mutations (a write of the live source, a missing retry bound) violate them.
- `generated/`: the generated model (`Model.qnt` for Quint, `model.yaml` as a TransitionModel document), its LikeC4 view, the extracted process and the generated replay projection, environment and boundary gating (`gen_test.go` regenerates the Go files with `-regenerate-adapter`).
- `adapter.go`: fixtures, client wiring and routing of runtime events to the worker; everything else it replays through is generated.
- `evidence.go`: targeted scenarios, replay of the observed variables, mutation detection and the mutation runner; `detect/` is the command a mutated build runs.
- `mutations.yaml`: the `direct-source` and `wrong-write` mutations and the observed variable expected to detect each.
- `traces/`, `report.yaml`, `receipt.yaml`: 40 Quint traces of the generated model, the report (coverage, observed variables, the abstraction's bounds and excludes verbatim, the mutation matrix, exact tool versions) and the input-closure receipt.

From the repository root, run `make library-demo` to regenerate the model and the evidence (needs Quint and the go command), or `go test ./examples/configmirror` to replay without Quint. See `../../LIBRARY.md` for library APIs and scope.

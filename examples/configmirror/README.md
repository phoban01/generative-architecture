# ConfigMirror library example

This ordinary controller copies the source ConfigMap's `data.value` into a pre-existing target ConfigMap. It imports only the public library. It is the Checkpoint A example: its behaviour model is generated from the controller source, and the evidence is sampled from that generated model. Start with `../../START-HERE.md` for the intended automatic-generation workflow, shown on `../database/`.

- `controller.go`: actual reconciler.
- `project.yaml`, `abstraction.yaml`, `constraints.yaml`, `discovery.json`: the authored inputs (module and package, bounded ConfigMap abstraction with its bounds and excludes, independent properties written against the generated names, API discovery snapshot). `inputs_test.go` checks they load and agree with the controller, the adapter fixtures, the generated model and the saved traces.
- `generate.go`: derives the TransitionModel from the typed extraction of `Reconcile`, the abstraction, the built-in client summaries and the controller-runtime profile.
- `generated/`: the generated model (`Model.qnt` for Quint, `model.yaml` as a TransitionModel document), its LikeC4 view, the extracted process and the generated replay projection, environment and boundary gating (`gen_test.go` regenerates the Go files with `-regenerate-adapter`).
- `adapter.go`: fixtures, client wiring and routing of runtime events to the worker; everything else it replays through is generated.
- `evidence.go`: targeted scenarios, replay of the observed variables, mutation detection and the mutation runner; `detect/` is the command a mutated build runs.
- `mutations.yaml`: the `direct-source` and `wrong-write` mutations and the observed variable expected to detect each.
- `traces/`, `report.yaml`, `receipt.yaml`: 40 Quint traces of the generated model, the report (coverage, observed variables, the abstraction's bounds and excludes verbatim, the mutation matrix, exact tool versions) and the input-closure receipt.
- `system.yaml`: the former handwritten model, kept only for the parity test in `generate_test.go` until it is deleted.

From the repository root, run `make library-demo` to regenerate the model and the evidence (needs Quint and the go command), or `go test ./examples/configmirror` to replay without Quint. See `../../LIBRARY.md` for library APIs and scope.

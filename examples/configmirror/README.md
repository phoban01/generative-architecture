# ConfigMirror library example

This ordinary controller copies the source ConfigMap's `data.value` into a pre-existing target ConfigMap. It imports no machine-vendor code. Its behaviour model is handwritten: this is an observer/runtime integration test, not the example of source-generated behaviour. Start with `../../START-HERE.md` for the intended automatic-generation workflow.

- `controller.go`: actual reconciler.
- `system.yaml`: independent KRM/CEL behaviour, properties and effect bindings.
- `adapter.go`: fixtures, injected clients, environment events and observed projection.
- `adapter_test.go`: targeted replay, recorded Quint traces, cancellation and mutations.
- `generated/`: Quint and LikeC4 outputs.
- `traces/`, `report.yaml`: bounded simulation evidence.

From the repository root, run `make library-demo` to regenerate evidence, or `go test ./examples/configmirror` to replay without Quint. See `../../LIBRARY.md` for library APIs and scope.

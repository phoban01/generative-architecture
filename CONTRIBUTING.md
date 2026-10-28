# Contributing

## Issues

Work is tracked as small GitHub issues sized for one change each. Every issue title carries a key such as `[M0-02]`, where `M<n>` is the milestone from [PROJECT-PLAN.md](PROJECT-PLAN.md) and `<k>` orders the issue within it. Issues are labelled with the area they touch (`area:*`), the requirement IDs they serve (`req:*`) and `agent-ready` once they are specified well enough to be picked up without discussion. An issue body lists the issues it depends on; pick one whose dependencies are closed.

## Changes

- Every change needs a test. Add or extend a Go test in the package you change, or a replay fixture under `examples/` when the behaviour is only observable through a generated model.
- Run `go build ./... && go vet ./... && go test ./...` before opening a pull request. `make check` additionally replays the hash-checked evidence of the sample controller.
- When you change `.github/workflows/`, the `Makefile` CI targets or `pkg/envtest`, run `make envtest` and `make arch-pipeline`. GitHub runs the workflows; `hack/ci` checks their shape in `go test`.
- Keep domain vocabulary out of `pkg/`, `cmd/` and the intermediate representation. Examples illustrate concepts with neutral sample controllers (see [ADR-0004](docs/adr/0004-reference-system.md)).
- Record a decision that changes the plan or the public surface in `docs/adr/`.

## Evidence

Generated models, ITF traces and `report.yaml` receipts under `examples/` are committed on purpose. Each receipt stores the hashes of its inputs, so the native tests fail when a model, constraint or source file changes without the evidence being refreshed. When your change alters any of those inputs, run `make verify` and commit the refreshed files. `make verify` needs Quint and LikeC4; `devbox shell` provides both at the pinned versions (see [README.md](README.md#development-environment)).

## Releases

[docs/release.md](docs/release.md) lists the release steps, their state and the compatibility policy. A test checks that every file it names exists.

## Licence

Contributions are accepted under the [Apache License 2.0](LICENSE).

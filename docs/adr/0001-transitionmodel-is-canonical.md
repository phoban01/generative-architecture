# ADR-0001: TransitionModel is the canonical model schema

Status: accepted (2026-10-13)

## Context

Two model schemas coexist: the library's KRM `TransitionModel` (`pkg/model`), and the
`Contract`/`System` schema used by `system.yaml` and `contracts/*.yaml`, which carries
example-specific constructs (`domain: INFRAS`, `Assignment` records, action `params`).
`KRM.md` records that they are not interchangeable.

## Decision

`TransitionModel` is the only schema consumed by generators, composition and replay.
The `Contract`/`System` schema is legacy, confined to `cmd/machinevendor` and its example
directory, and receives no new features.

## Consequences

- New work targets `pkg/model`, `pkg/composition` and `pkg/ir`.
- `cmd/arch` becomes the generic CLI and must not import `examples/`.
- Features the legacy schema has and `TransitionModel` lacks (parameterised actions, finite
  domains with quantifiers) are added to `TransitionModel` when a requirement needs them.

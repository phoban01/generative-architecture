# ADR-0001: TransitionModel is the canonical model schema

Status: accepted (2026-10-13); amended 2026-10-14

## Context

The prototype originally carried two model schemas: the library's KRM `TransitionModel`
(`pkg/model`), and a `Contract`/`System` schema used by its first domain example, which
had example-specific constructs (finite named domains, fixed record types, action
`params`). The two were not interchangeable.

## Decision

`TransitionModel` is the only schema consumed by generators, composition and replay.
The `Contract`/`System` schema and the example that used it were removed from the
repository on 2026-10-14; no legacy schema remains.

## Consequences

- New work targets `pkg/model`, `pkg/composition` and `pkg/ir`.
- `cmd/arch` is created as the generic CLI in M0 and must not import `examples/`.
- Features the removed schema had and `TransitionModel` lacks (parameterised actions, finite
  domains with quantifiers beyond `all()`) are added to `TransitionModel` when a requirement
  needs them.

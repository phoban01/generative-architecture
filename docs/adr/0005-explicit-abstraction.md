# ADR-0005: State abstraction is a declared input

Status: accepted (2026-10-13)

## Context

Extracting watches, API calls and control flow does not by itself produce a bounded
executable model. Something must decide which object fields and locals enter the model,
how object populations and string values are bounded, how identity and references are
preserved across resources and clusters, and how real API state projects back onto model
state during replay. In the prototype these decisions are spread across hand-written
`model.yaml` state and adapter `Project()` code, so they are neither reviewable nor reused.

## Decision

An `Abstraction` KRM document (`pkg/abstraction`) declares, per resource: the selected
JSON paths and their model types, population bounds, value domains, identity keys and
reference fields. The same document drives model state generation and generated
projection code. Frontends and constraints may propose fields; the document records the
choice. Every report repeats the bounds and lists behaviours the abstraction preserves,
excludes or over-approximates. A bounded result is never reported as unqualified.

## Consequences

- `pkg/abstraction` is delivered in M1 and validated at Checkpoint A.
- Generated projections read implementation state only; they never consult expected
  model state.
- Changing an abstraction invalidates evidence like any other input.

# ADR-0002: One shared IR between frontends and generators

Status: accepted (2026-10-13)

## Context

Each frontend returns its own result type (`goflow.Program`, `watchgen.Result`,
`dynamicgen.Result`, `sourcegen` descriptors, `contracts.Evidence`) and each generator reads
one of them directly. Provenance, evidence kind and identity are represented differently in
each, and diagrams are emitted from five places.

## Decision

`pkg/ir` defines versioned records for identity, controllers, processes, effects, watches,
predicates, key mappings, contracts, constraints, provenance and evidence kind. Frontends emit
IR; generators (Quint, LikeC4, adapters, reports, diff) read only IR.

## Consequences

- Stable IDs are derived from content hashes and source locations, never from slice indices.
- The IR has a schema version and a migration policy; generated artifacts record it.
- Existing frontends are adapted to emit IR before new frontends are written.

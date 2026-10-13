# ADR-0003: Quint and LikeC4 stay external and are accepted by version range

Status: accepted (2026-10-13)

## Context

The Go tools invoke the Quint and LikeC4 CLIs and fail closed on exact versions
(Quint 0.33.0, LikeC4 1.59.4). Go 1.25 has already left nixpkgs as end of life. Exact pins
break adopters on every patch release.

## Decision

Quint and LikeC4 remain separately installed executables selected by `QUINT_BIN`/`LIKEC4_BIN`
or flags. Each is accepted within a documented semver range (minimum version plus the next
minor as the exclusive upper bound). The repository has no npm project. The supported Go
toolchain is the two most recent minor releases.

## Consequences

- Version checks parse semver and compare ranges; receipts record the exact version used.
- CI runs a small matrix across the supported range.

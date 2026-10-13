# ADR-0004: A neutral reference system drives exit criteria

Status: accepted (2026-10-13)

## Context

The existing examples carry a machine-vendor domain (MAC, claims, leases). They are useful
regression fixtures but make the library look domain-specific and are not a credible
adoption walkthrough for other teams.

## Decision

`examples/refsystem` is a neutral multi-project system: three owned controllers
(`tenant`, `database`, `backup`) that interact only through Kubernetes resources, plus a Flux
`Kustomization` boundary model. It is the README walkthrough and the subject of every
milestone exit criterion. The machine-vendor, assignment and ConfigMirror examples remain as
fixtures under `examples/` and `cmd/machinevendor` until the reference system covers their
regression value, after which they may be removed.

## Consequences

- No domain vocabulary enters `pkg/`, `cmd/arch` or the IR.
- The reference system grows one controller at a time, each added by a small issue.

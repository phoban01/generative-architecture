# ADR-0004: A neutral reference system drives exit criteria

Status: accepted (2026-10-13); amended 2026-10-14

## Context

The prototype's first example carried a vendor-specific domain that made the library look
domain-specific and was not a credible adoption walkthrough for other teams. On 2026-10-14
that example and its legacy stack were removed. The remaining examples are neutral: the
sample database controller (`examples/database`, source-derived), ConfigMirror
(handwritten model) and the runtime group profile. They are useful regression fixtures but
each covers one controller, not a composed system.

## Decision

`examples/refsystem` is a neutral multi-project system: three owned controllers
(`tenant`, `database`, `backup`) that interact only through Kubernetes resources, plus a Flux
`Kustomization` boundary model. It is the README walkthrough and the subject of every
milestone exit criterion. The sample database controller, ConfigMirror and runtime group
examples remain as fixtures under `examples/` until the reference system covers their
regression value, after which they may be removed.

## Consequences

- No domain vocabulary enters `pkg/`, `cmd/arch` or the IR.
- The reference system grows one controller at a time, each added by a small issue.
- The sample database controller may be absorbed into the reference system's `database`
  controller rather than maintained separately.

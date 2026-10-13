# ADR-0006: Integration checkpoints gate surface expansion

Status: accepted (2026-10-13)

## Context

Discovering architecture is easier than running an existing controller under generated
instrumentation without bespoke integration code. A backlog of small, package-scoped
issues can be closed one by one while the assembled pipeline still models the wrong
behaviour. The 2026-10-13 plan review asked for the complete pipeline to be proved early
on an existing controller.

## Decision

Two integration checkpoints are first-class deliverables with their own issues:

- **Checkpoint A, first complete flow:** one existing in-repository controller
  (`examples/configmirror`) goes from source to generated behaviour, effects and watches,
  a bounded model from an abstraction document, Quint exploration, generated replay
  integration and a detected behavioural regression, including dynamic-client access.
- **Checkpoint B, external adoption:** one pinned upstream controller that was not written
  for the extractor is extracted, modelled and either replayed or reported as unsupported
  with located diagnostics; the adoption guide is validated against it.

Expansion of the client surface, upstream boundaries and diagram views waits for
Checkpoint A. Supported construction and dependency-injection patterns are documented at
Checkpoint A; extraction fails closed on others, and project-specific setup is never hidden
in a library helper.

## Consequences

- A narrow slice of adapter generation moves from M5 into Checkpoint A.
- Checkpoint issues depend on the minimal subset of M1 and M2 issues, not on whole
  milestones.
- Findings from each checkpoint are recorded as an ADR before dependent issues proceed.

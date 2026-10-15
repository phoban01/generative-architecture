# ADR-0007: Checkpoint A findings

Status: accepted (2026-10-15)

## Context

ADR-0006 made Checkpoint A the first complete flow: an existing controller goes from source
to a generated model and generated replay with a detected regression, including dynamic-client
access. Issues #121 to #129 delivered it on ConfigMirror and on the sample database
controller. This record captures what the abstraction and the adapter interfaces could and
could not express, so that the issues gated on the checkpoint start from the real shape of
the pipeline rather than the planned one.

## Result

ConfigMirror is source-generated end to end. The typed frontend extracts `Reconcile` with
callee-resolved calls and no statement-text callbacks. The model is built from the
extracted program, `abstraction.yaml` and the built-in Get and Update summaries. Projection,
environment and boundary gating are generated. Forty fresh Quint 0.33.0 traces replay
through the generated adapter, the `wrong-write` and `direct-source` mutations from a
`MutationSuite` are detected, and the receipt lists the bounds, excludes and exact tool
versions. Before the handwritten model was deleted, a parity test found no differences on
any of its actions across 2400 trace states.

The sample database controller replays through a projection generated from its
abstraction over `dynamic.Interface`. Its model is still the handwritten `model.yaml`.

## What the abstraction document could not express

- **Map keys as path elements.** Annotation keys contain `.` and `/`. The abstraction now
  allows an arbitrary key only as the third element under `metadata.annotations` or
  `metadata.labels`, and such a field must declare a `name`. Keys elsewhere, and in
  `identity` and `references` paths, are still identifier-only.
- **Derived booleans.** `contractSatisfied` is an annotation compared with a literal. The
  abstraction gained `equals` on `bool` fields, string equality only. Environment
  generators refuse to set such a field.
- **Partial map writes.** Assigning a map literal updates only the keys it lists; keys the
  abstraction selects but the literal omits are not cleared in the model.
- **Reference validity.** The database adapter checks by hand that `spec.databaseName`
  names the right object. Reference fields are declared but no check is generated.

## Adapter interfaces that changed

- `ApplyEnvironment` takes a `meta.RESTMapper`, because generation has no catalog to
  resolve GVRs; resolution happens at run time like `Project`.
- Typed readers got their own generators (`GenerateReaderProjection`,
  `GenerateReaderEnvironment`, `GenerateBoundaryGating`); the cached reader is a second
  `client.Reader` and its values appear as `cached_*` variables.
- Environment events are generated per value of the field's domain.
- The controller-runtime worker reports `Dirty` and `Processing` so the adapter no longer
  records them.

Handwritten glue remaining in ConfigMirror's `adapter.go`: fixture construction, client
wiring, a six-entry table from runtime events to worker methods, a no-op for internal
process steps and the names of the six worker variables. The worker table is a candidate
for generation from `pkg/controllerruntime`'s group model. ConfigMirror also keeps
`generate.go`, which holds its extraction options and declared runtime settings (one
worker, retry after two ticks, a 20-tick clock), and `evidence.go`; ADOPT-1 needs both
expressed in the `Project` document.

## Summaries and extractor overrides

No business summary or hand-written override of a built-in summary was needed. The
extractor needed additive constructs, each failing with a located diagnostic when
unsupported: pointer-literal declarations, string-keyed map entries, field assignment on
locals, `return …, client.Update(...)`, zero-value returns and a node's error result.
The `notFound` and `conflict` outcomes are unreachable under ConfigMirror's abstraction and
are left out of its gating; a test asserts no trace reaches them.

## Gaps that need a decision

- **Receipt drift scope.** ConfigMirror's receipt drift check only covers paths under
  `examples/configmirror/`, so a change in `pkg/` that alters the generated model does not
  mark the evidence stale. This contradicts EVID-3's input closure. Either the drift check
  covers the whole package closure and every library change refreshes evidence with Quint,
  or the closure is split into example inputs (fail) and library inputs (warn).
- **Mutation detection path.** Both mutants are caught by replay, because the observed
  value differs from the model. A constraint fails only when the model is rebuilt from the
  mutated source, which needs overlay support in `goflow.Load`.
- **Coverage.** The 40 traces reach the successful write step three times. Sampling should
  be steered towards process completion, as the seeded walks in `generate_test.go` are.
- **Database model.** The sample database controller still maps abstraction names onto
  handwritten `model.yaml` names. Generating its model is the same work as ConfigMirror's.

## Consequences

- Issues gated on Checkpoint A (client-surface expansion, upstream boundaries, diagram
  views) may proceed.
- New issues: overlay support in `goflow.Load`; generated reference checks; map keys in
  identity and reference paths; full-map replacement on map-literal writes; generated
  worker-event routing; trace sampling steered to completion; generating the database
  sample's model; and a decision on receipt drift scope.

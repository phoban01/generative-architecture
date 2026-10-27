# ADR-0008: Checkpoint B findings

Status: accepted (2026-10-27)

## Context

ADR-0006 made Checkpoint B the first adoption of a controller that was not
written for the extractor. Issues #131 to #134 pinned the kubebuilder
CronJob tutorial controller (v4.9.0) under `testdata/external/cronjob`,
ran the pipeline on the unmodified source, wrote its abstraction and walked
a draft adoption guide (`docs/ADOPTION.md`) against it. This record
captures what adoption required, what failed closed and which gaps to close
first.

## Result

The checkpoint ends on its unsupported-report branch. `arch extract` fails
closed at the setup preamble, `if r.Clock == nil { r.Clock = realClock{} }`
at `cronjob_controller.go:553`. It writes only `support.yaml`. `arch
generate` and `arch check` refuse to run without an IR, and `arch replay`
finds no traces. No model, trace or receipt exists.

Committed for the checkpoint:

- `generated/support.yaml`: one included controller and one located
  unsupported construct.
- `abstraction.yaml`: the CronJob and its Jobs as an adopter would select
  them.
- `report.yaml`: a `VerificationReport` with result `incomplete`, four
  unsupported findings and the hashes of its inputs. Nothing is reported
  as passed.
- `FOLLOWUPS.md`: twenty located gaps. Gaps 1 to 15 come from extraction
  and from the typed frontend run on a scratch copy (#132). Gaps 16 to 20
  come from writing the abstraction and from running `arch generate` past
  the missing IR in a scratch test.

Tests in `cmd/arch/external_test.go` check the upstream hashes, build the
module offline, rebuild both reports from CLI runs and repeat every command
of the adoption guide.

The exit criterion asks for a committed generated model. That part is not
met. The model needs the gaps in tier 1 below closed.

## What adoption required

1. A `go.mod` for the copied source. The pin moved controller-runtime from
   v0.22.1 to v0.23.3 so tests build from the local module cache. The
   source built unchanged.
2. `arch init` with the module, two packages and a name.
3. A `catalog` section, written by hand: the upstream CRD and a discovery
   snapshot with the `batch/v1` Job only. A full snapshot also lists
   `CronJob`, and the API package and `k8s.io/api/batch/v1` are both named
   `v1`, so the match by kind and package name becomes ambiguous.
4. An abstraction document and a controller declaration, both written by
   hand.

No Go code was written for the project. No marker was needed.

Project fields used: `module`, `packages`, `catalog.crds`,
`catalog.discovery`, `constraints` (empty), `runtime.profile`,
`runtime.maxTicks`, `abstraction` and `controllers` (`receiver`, `name`,
`workers`, `retryTicks`, `key`, `effects`). `bounds`, `boundaries`,
`initial`, `objectKeys` and `locals` were not needed so far. Whether the
model needs them is unknown until `arch generate` runs.

## What failed closed and why

- **The setup preamble.** `watchgen` accepts only builder chains and
  unstructured object declarations before them. A receiver default (line
  553) and a field index registration (line 557) are both rejected. The
  reason text names the accepted form, not the problem.
- **Generation limits.** Run past the missing IR, `arch generate` rejects
  `List`, `Create` and `Delete` effects. It accepts only `Get` and
  `Update`. With those removed, it rejects more than one selected field per
  resource. With both removed, the first `Reconcile` diagnostic is a
  non-zero `ctrl.Result` at line 528.
- **Every later command.** `generate`, `check`, `replay` and `render` each
  refuse with a message that names the missing input.

`arch extract` fails in inventory mode too. Inventory mode only changes how
`support`, `generate`, `check` and `replay` treat unsupported findings.

## What the abstraction could not express

- **Lists.** `status.active` is a list of object references. The model sees
  the Job population instead.
- **Optional and time values.** `spec.suspend` is a `*bool` and
  `status.lastScheduleTime` a `*metav1.Time`. The author mapped them to a
  `bool` and to a `string` domain of three instants.
- **Owner references.** `ownerReferences` is a list, and the controller
  owner is the entry with `controller: true`. The loader accepts
  `[metadata, ownerReferences, name]` as a scalar path. The generated
  reference check would read it as a scalar and report a valid Job as
  broken.
- **Derived names.** The controller names a Job `<cronjob>-<unix time>`.
  The population needs fixed names, so the author picked two runs.

`abstraction.Load` accepts the document. Only `arch generate` would reject
it, after extraction, for its three CronJob fields.

## Gaps ranked by how many controllers they block

Numbers refer to `testdata/external/cronjob/FOLLOWUPS.md`.

**Tier 1: the kubebuilder scaffold defaults.** Nearly every kubebuilder
project hits these.

- Gap 5: client methods promoted through an embedded `client.Client`. The
  scaffold embeds it. No `Reconcile` that calls `r.Get` extracts today.
- Gaps 1 and 2: non-watch statements in `SetupWithManager`. A receiver
  default, a field indexer or a logger set-up stops extraction before any
  watch is read.
- Gap 6: `logf.FromContext` and `logr.Logger` calls. The scaffold
  `Reconcile` starts with one.
- Gap 13: `var obj T` before `r.Get(ctx, req.NamespacedName, &obj)`. The
  book and most scaffolds use it.
- Gap 14: `client.IgnoreNotFound` and `ctrl.SetControllerReference`.
- Gap 16: `Create`, `Delete` and `List` in `arch generate`. The summaries
  exist in `pkg/summaries`; the model bindings do not.
- Gap 3: extraction stops at the first diagnostic. It blocks nothing by
  itself, but it costs one run per gap.

**Tier 2: common in real controllers.**

- Gap 7: `ctrl.Result{RequeueAfter: ...}`.
- Gap 9: `range` over listed items.
- Gap 12 and 18: pointer and optional fields, lists and time values.
- Gap 17: one selected field per resource in `arch generate`.
- Gap 19: owner references. Every controller that uses `Owns` has one.
- Gap 15: list options dropped from the effect. This one is silent, not
  fail-closed, so it needs a fix before `List` is bound (gap 16).

**Tier 3: specific to this controller or rare.**

- Gap 8: function literals bound to locals.
- Gap 10: `len` of a local slice.
- Gap 11: the clock and time arithmetic.
- Catalog matching by Go package name. It fails only when two catalog
  entries share a kind and a version.

Smaller findings: `arch generate` after a failed extract says "run arch
extract first" (gap 20), and the `arch support` table breaks on a
multi-line construct.

## Interface changes

None. Checkpoint B changed no package API and no document schema. The
pipeline report in `report.yaml` is built by the test from CLI results,
because no `arch` command writes a `VerificationReport` when the pipeline
stops before `arch check`.

## Gaps that need a decision

- **Should `arch extract` read `Reconcile` without an abstraction?** Today
  it reads only `SetupWithManager`, so the support report says nothing
  about the reconcile body (gap 4). Recommendation: extract anyway. Run the
  typed frontend with the built-in summaries and no field mapping. Report
  every object field it reads or writes as `unresolved`, and every
  construct it cannot read as `unsupported`. The author then sees all gaps
  before writing an abstraction. The IR still needs no abstraction; only
  `arch generate` does.
- **Diagnostics after the first.** Collecting one diagnostic per statement
  needs a rule for what the extractor skips after an error. Recommendation:
  skip the statement, keep going, and write no IR while any diagnostic
  exists.
- **CI before extraction is clean.** The reusable workflow fails at its
  Extract step in both modes. An adopter cannot run it as a non-blocking
  inventory. Options: an input that runs `arch support` and uploads the
  report when extraction fails, or a documented separate job. The draft
  guide documents the separate job.
- **Where to validate the abstraction.** The loader accepts documents that
  `arch generate` rejects (several fields per resource, list paths). Either
  `abstraction.Load` checks generator limits, or `arch support` validates
  the abstraction against the catalog and the generator before
  extraction ends. Recommendation: `arch support`, so the loader stays a
  schema check.
- **A pipeline report when the pipeline stops early.** Either `arch check`
  writes an `incomplete` report that names the missing input, or the
  workflow uploads `support.yaml` alone.
- **The clock.** Gap 11 needs a rule for how the model treats time: an
  environment choice per time-dependent condition, or a bounded clock
  variable.

## Consequences

- Checkpoint B stands on its unsupported-report branch. The generated model
  for the CronJob controller waits on the tier 1 gaps.
- Issues gated on Checkpoint B may proceed. The tier 1 gaps should come
  before the expansion of the diagram and composition surface.
- `docs/ADOPTION.md` is the draft guide for #110. A test repeats each of
  its commands, so a change in a message breaks the build.
- New issues to file: one per tier 1 gap (5, 1 and 2, 6, 13, 14, 16, 3);
  extract `Reconcile` without an abstraction (gap 4, after the decision);
  several fields per resource in `arch generate` (17); an owner reference
  form in the abstraction (19); list options in the `List` effect (15);
  abstraction validation in `arch support`; an inventory path in the
  reusable workflow; and catalog matching by API group.

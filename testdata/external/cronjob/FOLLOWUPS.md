# Checkpoint B follow-ups: CronJob tutorial controller

`arch extract` on this controller fails closed. `generated/support.yaml`
lists one unsupported construct. No `ir.yaml` exists, because the extractor
writes no IR while a construct is unsupported. A test in
`cmd/arch/external_test.go` fails when the committed report drifts.

`abstraction.yaml` (#133) is the abstraction an adopter would write for this
controller. `report.yaml` is the `VerificationReport` of the whole pipeline:
`extract` fails closed, and `generate`, `check` and `replay` refuse to run.
Its result is `incomplete`. Gaps 16 to 20 come from writing the abstraction
and from running the `generate` frontend past the missing IR in a scratch
test.

Line numbers refer to `internal/controller/cronjob_controller.go` at the pin.

## How the gaps were found

- Gaps 1 and 2 come from `arch extract` and `arch support`.
- `goflow` and `watchgen` stop at the first diagnostic. To find the next
  gap, a scratch copy of the controller had the reported construct removed,
  and extraction ran again. The scratch copy kept the line numbers. Nothing
  in this directory was changed.
- When this list was written, `arch extract` read only `SetupWithManager`.
  Gaps 4 to 13 came from running the typed `Reconcile` frontend
  (`goflow.ExtractTyped`) through `examples/refsystem/internal/extractcheck`
  on the scratch copy. Since #103, `arch extract` and `arch support` run
  that frontend on every `Reconcile` and collect every diagnostic, so
  `generated/support.yaml` now lists these gaps directly (55 unsupported
  items). A diagnostic there means the frontend cannot read the construct at
  all.
- Gap 14 is from reading the code. The probe removed these constructs
  together with others, so no diagnostic text is recorded for them.

## Draft issues

Each draft names the construct, the location, the diagnostic and the
smallest extension that would support it.

### 1. Setup preamble: default value for a receiver field

- Construct: `if r.Clock == nil { r.Clock = realClock{} }` before the
  builder chain.
- Location: line 553 (reported by `arch extract`).
- Diagnostic: `preamble: expected an unstructured object declaration`.
- Problem: the statement has no watch effect. The reason text is also
  misleading, because it names the only preamble form that `watchgen`
  accepts and not the problem.
- Smallest extension: in `watchgen`, accept an `if` statement that only
  assigns a literal to a receiver field when the field is nil. List it as a
  summarized `setup` item ("receiver default; no watch effect"). Change the
  reason to "unsupported setup preamble statement; only unstructured object
  declarations are supported".

### 2. Setup preamble: field index registration

- Construct:
  `if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kbatch.Job{}, jobOwnerKey, func(...) []string {...}); err != nil { return err }`.
- Location: line 557. Gap 1 hides it.
- Diagnostic: `preamble: unsupported setup preamble`.
- Problem: kubebuilder projects often register an index next to `Owns`.
  `Reconcile` reads it through `client.MatchingFields{jobOwnerKey: ...}`
  (line 132).
- Smallest extension: recognize this exact `IndexField` form with a
  `&T{}` object and a constant key. Record a summarized `index` item with
  the resource and the key. Keep the extractor function summarized.

### 3. Extraction stops at the first diagnostic

- Construct: any second unsupported statement in `SetupWithManager` or
  `Reconcile`.
- Location: the report shows line 553 only; line 557 stays hidden.
- Diagnostic: none; the second gap is not reported.
- Problem: an author fixes one gap and finds the next one only on the next
  run. Checkpoint A recorded the same problem for the database and backup
  controllers.
- Smallest extension: in the `watchgen` preamble, collect one diagnostic per
  statement and go on with the next statement. Do the same per top-level
  statement in `goflow`. Report all of them.

### 4. arch extract does not read Reconcile (closed by #103)

- Construct: the `Reconcile` method (line 100).
- Location: `cmd/arch/extract.go`, which at the time extracted only
  `SetupWithManager`.
- Resolution: `arch extract` and `arch support` now run `goflow.ExtractTyped`
  on every `Reconcile` with the built-in client summaries. Without an
  abstraction document, each field the method reads or writes is reported
  as a located `unresolved` item, as ADR-0008 recommended. Every diagnostic
  reaches the support report.

### 5. Client methods promoted through an embedded client.Client

- Construct: `r.Get`, `r.List`, `r.Status().Update`, `r.Delete`, `r.Create`
  on a reconciler that embeds `client.Client`. This is the kubebuilder
  scaffold default.
- Location: lines 116, 132, 259, 287, 306, 453 and 512 (512 is reported
  first).
- Diagnostic: `call: unsummarized call to sigs.k8s.io/controller-runtime/pkg/client.CronJobReconciler.Create; declare it as a client operation or an external effect`.
- Problem: `goflow.calleeOf` takes the receiver name from the selection
  (`CronJobReconciler`) and the package from the method (`client`). The
  callee matches neither the reconciler nor the client interface.
- Smallest extension: for a promoted method (`len(sel.Index()) > 1`), name
  the receiver after the declaring type of the method (for `Create`,
  `client.Writer`). Calls through `r.Client.Create` already work.

### 6. Logging calls

- Construct: `log := logf.FromContext(ctx)`, `log.Error(...)`,
  `log.V(1).Info(...)`, `log = log.WithValues(...)`.
- Location: line 101 and 21 more lines; line 517 is reported first.
- Diagnostic: `call: unsummarized call to github.com/go-logr/logr.Logger.Info; declare it as a client operation or an external effect`.
- Smallest extension: a built-in no-effect summary for `logr.Logger`
  methods and for `log.FromContext` in controller-runtime.

### 7. Non-zero reconcile.Result

- Construct: `scheduledResult := ctrl.Result{RequeueAfter: nextRun.Sub(r.Now())}`
  and `return scheduledResult, nil`.
- Location: line 412; returns at lines 422, 434, 446, 508 and 528 (528 is
  reported first).
- Diagnostic: `return: result must be the zero value of its type`.
- Smallest extension: accept `ctrl.Result{RequeueAfter: <expr>}` (directly
  or through one local) as a "requeue after" outcome. Summarize the duration
  as an unknown positive value.

### 8. Calls of function literals bound to locals

- Construct: helper closures `isJobFinished` (173),
  `getScheduledTimeForJob` (188), `getNextSchedule` (344) and
  `constructJobForCronJob` (475), called at lines 203, 215, 400 and 504.
- Location: line 504 is reported first, then line 400.
- Diagnostic: `assign: unsupported Go statement`.
- Smallest extension: treat a function literal that is bound once to a
  local and never reassigned like a function declaration. Extract its body
  at the call, or let the author summarize it by name in the Project
  document.

### 9. range loops

- Construct: `for i, job := range childJobs.Items`,
  `for _, activeJob := range activeJobs` and similar.
- Location: lines 202, 233, 283, 302 and 451 (451 is reported first).
- Diagnostic: `range: unsupported Go statement`.
- Smallest extension: support `range` over the `Items` of a listed object
  when the loop body holds only client operations and branches. Bound the
  iterations by `maxObjectsPerResource`.

### 10. len() of a local slice

- Construct: `len(activeJobs) > 0`.
- Location: line 444.
- Diagnostic: `expression: unsupported Go expression`.
- Smallest extension: model the length of a list result as a bounded
  integer. This depends on gap 9, because `activeJobs` is built in a loop.

### 11. Time values and time arithmetic

- Construct: `missedRun.IsZero()` (420), the local `tooLate` from
  `missedRun.Add(...).Before(r.Now())` (427 to 431), and `r.Now()` through
  the embedded `Clock` interface (400, 412, 413, 429).
- Location: line 431 is reported first, then line 420.
- Diagnostics: `identifier: unbound local variable tooLate` and
  `expression: unsupported Go expression`.
- Smallest extension: let the author declare a time-dependent condition as
  an environment choice (a nondeterministic boolean). This needs a decision
  on how the model treats the clock.

### 12. Optional and non-scalar fields

- Construct: `cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend` (a
  `*bool`), `cronJob.Status.LastScheduleTime = &metav1.Time{...}` (a
  `*metav1.Time`) and `cronJob.Status.Active = nil` (a slice of structs).
- Location: lines 321, 228 and 232.
- Diagnostics: `field: unmapped field cronJob.spec.suspend`,
  `field: unmapped field cronJob.status.lastScheduleTime` and
  `field: unmapped field cronJob.status.active`.
- Note: the probe maps only fields of basic type, so an abstraction document
  could select some of these. The nil check and the dereference of a
  pointer field still need support.
- Smallest extension: map a pointer to a basic type to an optional model
  value. Lower `p != nil` to "is set" and `*p` to its value.

### 13. Zero-value var declarations of objects

- Construct: `var cronJob batchv1.CronJob` and
  `var childJobs kbatch.JobList`, then `&cronJob` in client calls.
- Location: lines 115 and 131.
- Diagnostic: `declaration: unsupported Go statement`.
- Smallest extension: treat `var x T` of a struct type like `x := &T{}`.
  Checkpoint A added the pointer-literal form already, so this is small.

### 14. Library helpers without a summary

- Constructs and locations: `client.IgnoreNotFound` (121, 287, 453),
  `sort.Slice` (277, 296), `ctrl.SetControllerReference` (495),
  `ref.GetReference` (234), `cron.ParseStandard` (345), `fmt.Sprintf`
  (477), `DeepCopy` (486), `append` to a status slice (239) and map writes
  in a loop (488 to 494).
- Diagnostics recorded: `call: unsummarized call to sigs.k8s.io/controller-runtime/pkg/client.IgnoreNotFound; ...`
  (121) and `call: unsummarized call to sort.Slice; ...` (296). The others
  were removed together with other constructs.
- Smallest extension: built-in summaries for `client.IgnoreNotFound` (an
  error filter on `notFound`) and `ctrl.SetControllerReference` (sets the
  owner reference on a local). A pure-helper summary class for `sort`,
  `fmt`, `DeepCopy` and `GetReference`. `cron.ParseStandard` needs an author
  summary.

### 15. List options are not in the effect

- Construct: `r.List(ctx, &childJobs, client.InNamespace(req.Namespace), client.MatchingFields{jobOwnerKey: req.Name})`.
- Location: line 132.
- Diagnostic: none. The probe accepts the call and keeps no options.
- Problem: the namespace and the index selector decide which Jobs the
  reconciler sees. If the built-in List summary drops them as well, the
  model reads every Job. This is a possible silent loss, not a fail-closed
  gap.
- Smallest extension: record `InNamespace` and `MatchingFields` on the List
  node. Fail with a located diagnostic on other options.

## Gaps in the abstraction and in arch generate (#133)

The scratch test wrote the IR that `extract` builds before it fails, and
ran `arch generate` on it. Each finding below was removed in a scratch copy
of `project.yaml` or `abstraction.yaml` to find the next one. The committed
documents keep what an adopter would write.

### 16. arch generate supports Get and Update only

- Construct: the declared effects for `client.Reader.List`,
  `client.Writer.Delete` and `client.Writer.Create`.
- Location: `project.yaml`, `spec.controllers[0].effects`.
- Diagnostic: `controller cronjob: sigs.k8s.io/controller-runtime/pkg/client.Reader.List: method List is not supported; supported methods are Get and Update`.
- Smallest extension: built-in summaries and model bindings for `Create`
  and `Delete` on a declared object, and for `List` bounded by
  `maxObjectsPerResource`. Gap 15 applies to `List`.

### 17. arch generate takes one field per resource

- Construct: three fields of `CronJob` and two of `Job`.
- Location: `abstraction.yaml`.
- Diagnostic: `resource batch.tutorial.kubebuilder.io/v1/CronJob: generation supports exactly one selected field, got 3`.
- Problem: `abstraction.Load` accepts the document. Only `arch generate`
  rejects it, after extraction.
- Smallest extension: one model variable per selected field in the
  generated state, and field-local bindings for each.

### 18. No list, pointer or time types in the abstraction

- Construct: `status.active` (a list of object references),
  `spec.suspend` (a `*bool`), `status.lastScheduleTime` (a `*metav1.Time`)
  and the Job conditions.
- Location: `abstraction.yaml`.
- Diagnostic: none. The author maps them to `bool` and `string` domains by
  hand or leaves them out.
- Smallest extension: an optional marker on a basic field (gap 12), and a
  bounded set type whose elements are declared objects.

### 19. Owner references have no form

- Construct: `ctrl.SetControllerReference(cronJob, job, r.Scheme)` (495)
  and the field index on the controller owner (557).
- Location: `abstraction.yaml`, Job `references`.
- Diagnostic: none. The loader accepts
  `path: [metadata, ownerReferences, name]`, but `ownerReferences` is a
  list. The generated reference check reads the path as a scalar, so it
  would report a valid Job as broken.
- Smallest extension: an `owner` reference form that selects the entry
  with `controller: true` and checks its kind and name.

### 20. arch generate after a failed extract names the wrong step

- Construct: `arch generate` after `arch extract` failed closed.
- Diagnostic: `generated/ir.yaml not found; run arch extract first`.
- Problem: the author ran `arch extract`. The message should say that
  extraction failed and point to `support.yaml`.
- Smallest extension: when `support.yaml` exists beside the missing IR and
  lists unsupported items, name the first one in the error.

After gaps 16 and 17 were removed, the first `Reconcile` diagnostic was
gap 7 at line 528 (`result must be the zero value of its type`).

## What worked

- Discovery found `CronJobReconciler` (line 49) with no annotation.
- The builder chain `For(&batchv1.CronJob{}).Owns(&kbatch.Job{}).Named("cronjob")`
  extracts. With gaps 1 and 2 removed, the report lists two watches at lines
  576 and 577.
- `Status().Update`, `Get` and `Create` (through `r.Client`), and comparison
  of a field with a package constant (444) extract in `Reconcile`.
- The separate module loads through `project.Load` and `goflow.Load` with
  `GOPROXY=off`. No `replace` and no change to `pkg/` was needed.

## Adoption notes

What the author had to do:

1. Write `go.mod` for the module. Upstream code at controller-runtime
   v0.22.1 built unchanged at v0.23.3.
2. Run `arch init --module tutorial.kubebuilder.io/project --package ./internal/controller --package ./api/v1 --name cronjob`.
3. Add the `catalog` section by hand. `arch init` does not write one. It
   needs the CRD for `CronJob` and a discovery snapshot for `batch/v1` Job.
4. Write that snapshot by hand with the Job resource only. `arch extract`
   matches a Go type to the catalog by kind name and Go package name. Both
   `batch/v1` and this API package are named `v1`. A full `batch/v1`
   snapshot also lists `CronJob`, so the match would be ambiguous, and the
   watch would become an unknown resource.

Project fields used: `module`, `packages`, `catalog.crds`,
`catalog.discovery`, `constraints` (empty) and `runtime.profile`. No
`abstraction`, `controllers`, `bounds` or `initial` was needed to extract.

What failed closed: the setup preamble (gaps 1 and 2). `arch extract` exits
non-zero, writes only the support report and removes any old IR.
`arch support` in inventory mode exits zero and prints the same item.

Two adoption problems are not gaps in the Go subset:

- Catalog matching by Go package name (note 4 above). A smaller fix is to
  match on the API group from the package's `GroupVersion` variable or its
  `+groupName` marker.
- `arch init` writes no catalog section. An author must know that `extract`
  needs one before any watch resolves.

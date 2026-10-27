# Checkpoint B follow-ups: CronJob tutorial controller

`arch extract` on this controller fails closed. `generated/support.yaml`
lists one unsupported construct. No `ir.yaml` exists, because the extractor
writes no IR while a construct is unsupported. A test in
`cmd/arch/external_test.go` fails when the committed report drifts.

Line numbers refer to `internal/controller/cronjob_controller.go` at the pin.

## How the gaps were found

- Gaps 1 and 2 come from `arch extract` and `arch support`.
- `goflow` and `watchgen` stop at the first diagnostic. To find the next
  gap, a scratch copy of the controller had the reported construct removed,
  and extraction ran again. The scratch copy kept the line numbers. Nothing
  in this directory was changed.
- `arch extract` reads only `SetupWithManager`. Gaps 4 to 13 come from
  running the typed `Reconcile` frontend (`goflow.ExtractTyped`) through
  `examples/refsystem/internal/extractcheck` on the scratch copy. Those
  options summarize every controller-runtime client call and map every
  field of basic type. A diagnostic there means the frontend cannot read the
  construct at all.
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

### 4. arch extract does not read Reconcile

- Construct: the `Reconcile` method (line 100).
- Location: `cmd/arch/extract.go`, which extracts only `SetupWithManager`.
- Diagnostic: none. The support report lists the controller as included,
  but nothing in its `Reconcile` is in the report.
- Problem: the report says nothing about the main body of the controller.
  An author gets no list of the constructs that block a model.
- Smallest extension: run `goflow.ExtractTyped` on `Reconcile` in
  `arch extract` with the built-in client summaries, and add its
  diagnostics to the support report. This needs a decision on the default
  options when the project has no abstraction document.

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

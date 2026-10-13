# Extracting a process's behavior from Go

Run commands from the repository root.

This demo now extracts the body of MAC's existing `Publish` method into a control-flow model, generates Quint transitions, and checks separately authored requirements. It also creates a separate Go source copy with the stale-assignment check removed and extracts that copy through the same compiler. The live controller source is not modified.

## Why subreconcilers help

A subreconciler is a useful unit of analysis: it carries out a bounded process with identifiable inputs, API effects and results. `Publish` is used as that boundary here, without changing the controller structure. Its retained inputs include the assignment previously observed by its caller.

The extractor preserves separate API reads and writes within the process. It does not assume that entering a subreconciler makes its execution atomic. A future parent-controller model would separately describe invocation order, short-circuiting, requeue decisions, shared context and concurrency. This demo does not extract that parent orchestration yet.

## Select the process using existing markers

`behavior/publication.yaml` selects an action already identified by the structural extractor:

```yaml
apiVersion: architecture.local/v1alpha1
kind: BehaviorAnalysis
metadata:
  name: mac-publication
spec:
  component: mac
  action: publish
  allowStalePublication: false
```

The existing `+arch:controller=mac` and `+arch:action=publish` markers resolve the actual method and source location. No additional method annotation is needed. The configuration also contains the two independently authored CEL requirements described below.

## What is extracted

The current Go method contains:

```go
if observed.MachineID == "" ||
    (!r.AllowStalePublication && observed != assignmentOf(claim)) {
    return false, nil
}
```

The extractor reads the Go AST, follows supported branches and early returns, and lowers the predicate to:

```text
(observed.machineId == "") or
(not(false) and (observed != claim))
```

Here `claim` represents the assignment snapshot returned by the API read, and the configured mutation flag is specialized to `false`.

The Go statement that writes the local InfraMachine's machine ID supplies the actual update expression. It is not copied from `contracts/mac.yaml`. The subsequent status update is a distinct external effect.

Each extracted node records its Go text, source line, condition or update expression, and successor nodes. All model variables are explicitly framed on every transition.

## What is supplied as an environment model

External effects require semantics beyond the syntax of a call. This first implementation uses explicit summaries:

| Operation | Summary |
|---|---|
| Get MachineClaim through APIReader | On success, capture the current lease assignment; on failure, set an error |
| Get InfraMachine | On success, capture its currently published machine ID |
| SetNestedField on the known status path | Change the local object; succeed for the well-shaped demo resource |
| Status.Update | On success, commit the local machine ID; on failure, make no change |
| External lease revocation | Clear the live lease while retaining the process's snapshot |
| External lease grant | Assign either of two hosts with a monotonically increasing epoch bounded at two |

The API failure summary intentionally excludes ambiguous outcomes such as a committed write followed by a transport timeout. Cache lag on the InfraMachine read, conflicts, retries, deletion, cancellation and multiple simultaneous MAC invocations are not modeled here. Reads/writes are individual transitions; branch evaluations can also interleave with the modeled lease process.

Helper implementations and concrete resource identities used by these summaries are checked against reviewed shapes. Changing a helper fails extraction pending review. Unknown statements, loops, calls, expressions and write paths are rejected rather than silently dropped. This is a narrow AST compiler, not a general Go-to-Quint translator or an SSA analyzer.

## Independent requirements and observed results

The requirements come from `behavior/publication.yaml`; neither is generated from the Go condition. Both apply at the status write's commit event.

**`publicationUsesCheckedLease`** requires a nonempty published ID matching the lease snapshot MAC checked, with the observed assignment and epoch matching that snapshot.

**`publicationUsesLiveLease`** requires the same consistency with the live lease at the instant of commit.

The demo produces these results:

| Source and schedule | Requirement | Result |
|---|---|---|
| Intact source, 500 sampled executions | Checked lease | No violation found |
| Intact source, lease revoked before read | Checked lease | Rejected stale publication; requirement holds |
| Intact source, same host reassigned with newer epoch before read | Checked lease | Rejected stale publication; requirement holds |
| Source with equality check deleted, lease revoked before read | Checked lease | Counterexample |
| Source with equality check deleted, same host at newer epoch | Checked lease | Counterexample |
| Intact source, lease revoked after read | Live lease | Counterexample |

The last result is important. The intact method can execute:

1. Read the current lease into a snapshot.
2. Have that lease revoked by another actor.
3. Validate its old observation against its retained snapshot.
4. Commit the assignment to InfraMachine status.

This is a publication-time race in the extracted abstraction, distinct from the earlier prepared-provisioning-call counterexample. It is not a proven production bug. Any real fencing, cancellation or cross-object synchronization guarantee would need to be included in the environment and tested.

## Run and inspect

```sh
# Requires Go on PATH; extracts and writes models without running Quint:
go run ./cmd/behavior-demo --generate-only

# Requires Go and Quint 0.33.0; use QUINT_BIN or --quint to select its executable:
go run ./cmd/behavior-demo --quint /path/to/quint

go test ./...
```

The full demo command succeeds when all expected outcomes above are reproduced, including the expected violations. A successful demo run does not assert that the live-lease safety property holds. Checks are sampled Quint simulation and targeted schedules, not an exhaustive proof.

Paths below are relative to the working project:

| File | Purpose |
|---|---|
| `examples/machinevendor/controllerdemo/mac.go` | Actual Go process analyzed |
| `behavior/publication.yaml` | Process selection and independently authored requirements |
| `examples/machinevendor/behaviorgen/extract.go` | Supported Go AST statements/expressions and explicit effect summaries |
| `examples/machinevendor/behaviorgen/model.go` | Control-flow-to-Quint generation |
| `generated/behavior/Publication.yaml` | Extracted intact control-flow graph with Go source locations |
| `generated/behavior/Publication.qnt` | Intact implementation model |
| `generated/behavior/mac_mutant.go.txt` | Actual mutated Go source copy |
| `generated/behavior/PublicationMutant.qnt` | Model extracted from the mutated source |
| `reports/behavior/summary.yaml` | Results and configuration/source/model hashes |
| `reports/behavior/*.yaml` | Readable counterexample states |

Quint traces retain their required ITF JSON format. Configuration, extracted metadata and reports use YAML; the requirements use CEL.

The controller adapter independently exercises the coarse phase contract. [Process/runtime composition](COMPOSITION.md) now embeds these extracted nodes in the shared runtime model and replays schedules against the Go method paused at client boundaries. Pure branches and local updates are stuttering steps; reads, submitted writes, returns and runtime results are compared independently.

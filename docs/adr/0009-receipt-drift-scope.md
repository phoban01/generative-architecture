# ADR-0009: Receipt drift has an owned scope and a library scope

Status: accepted (2026-10-28)

## Context

EVID-3 asks for the full input closure of each evidence run. `pkg/receipt.Collect`
records that closure: the selected packages, their in-module imports, `go.mod`, `go.sum`
and the extra documents. The callers did not agree on what drift means. ConfigMirror's
receipt test failed only on drift under `examples/configmirror/` and ignored the rest.
`arch check` and `arch replay` blocked on drift of any file. ADR-0007 recorded this gap
and named two options. Option (a) fails on every drift, so each library change needs a
Quint refresh of all evidence. Option (b) splits the closure and fails only on the part
the run owns.

## Decision

We take option (b). The receipt records which inputs the run owns.

- Owned inputs are the files of the selected package patterns and the extra paths.
  `Collect` records the directory of each matched package and each extra path in
  `owned`. An entry owns the file it names and every file under it.
- Library inputs are every other file in the closure: in-module imports outside the
  selected packages, `go.mod` and `go.sum`.
- Drift of an owned input fails. The evidence no longer derives from its inputs. Tool
  version drift is owned, because the tools are what ran.
- Drift of a library input warns. `arch check` and `arch replay` print a warning on
  stderr and list it under `warnings` in the report. It does not change the result.
  The ConfigMirror receipt test logs it.
- A receipt without `owned` owns every file. Older receipts keep failing on all drift.
- Before a release tag, `make verify` refreshes every receipt in the repository. No
  drift of either kind remains in a tagged release.

## Consequences

- A library change no longer forces a Quint refresh of every example between releases.
- Between releases, committed evidence can lag behind library code. The warnings show
  where it lags.
- A Go caller that wants every drift to fail can set `Owned` to `.` after `Collect`.
- `Drift.Scope`, `receipt.Split` and `Verification.Warnings` carry the split to callers.

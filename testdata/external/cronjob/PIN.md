# Pinned upstream: kubebuilder CronJob tutorial controller

Checkpoint B runs the extractor on a controller that was not written for it
(ADR-0006). This directory holds that controller.

## Source

- Repository: https://github.com/kubernetes-sigs/kubebuilder
- Path: `docs/book/src/cronjob-tutorial/testdata/project`
- Tag: `v4.9.0`
- Commit: `5e331e74c7a25c8e8fc0d9d5c33c319b7268f395` (2025-09-19)
- Licence: Apache-2.0, copied unchanged into `LICENSE`. Each Go file keeps
  its upstream licence header.
- Fetched once on 2026-10-27 from `raw.githubusercontent.com`. Tests never
  fetch.

## Why this controller

- It is the controller of the kubebuilder book, so most controller authors
  know its shape.
- It has one reconciler, `CronJobReconciler`, with ordinary wiring:
  `For(&CronJob{})`, `Owns(&Job{})` and a field index on Jobs.
- Its `Reconcile` uses common controller-runtime calls: `Get`, `List` with
  a field selector, `Delete` with propagation, `Status().Update`, `Create`,
  `SetControllerReference` and a non-zero `RequeueAfter`.
- It is small (580 lines) but nobody wrote it with this extractor in mind.

## What is copied

`upstream.sha256` lists every copied file with its SHA-256. The files are
byte-identical to the pinned commit. A test in `cmd/arch` checks the hashes,
so an edit to the upstream source fails the build.

- `LICENSE` (repository root)
- `PROJECT`
- `api/v1/cronjob_types.go`, `api/v1/groupversion_info.go`,
  `api/v1/zz_generated.deepcopy.go`
- `internal/controller/cronjob_controller.go`
- `config/crd/bases/batch.tutorial.kubebuilder.io_cronjobs.yaml`

Left out: `cmd/main.go` and the webhook package (not part of the
controller), the Ginkgo and envtest tests (they need a control plane), and
the deployment manifests.

## Changes from upstream

The Go source is not changed. Only the module files differ:

- `go.mod` is new. It keeps the upstream module path
  `tutorial.kubebuilder.io/project`. It moves the dependencies to the
  versions of the main module: controller-runtime v0.22.1 to v0.23.3, and
  `k8s.io/api`, `apimachinery` and `client-go` v0.34.0 to v0.35.0. The
  source builds unchanged at these versions.
- `go.sum` comes from `go mod tidy` with these versions. The module has no
  `replace` directive.
- `github.com/robfig/cron v1.2.0` stays at the upstream version. It was
  downloaded into the module cache once, with the source.

## Files added for this project

- `project.yaml` and `constraints.yaml` come from
  `arch init --module tutorial.kubebuilder.io/project --package ./internal/controller --package ./api/v1 --name cronjob`.
  The catalog section is added by hand.
- `catalog/batch-v1.json` is a discovery snapshot with the `batch/v1` Job
  resource only. The CronJob kind comes from the upstream CRD. A snapshot
  that also lists `batch/v1` CronJob would make the kind `CronJob` in
  package `v1` match two catalog entries.

## Build

The directory is a separate module. The main module's `./...` does not
include it, because it is under `testdata`.

    cd testdata/external/cronjob && GOWORK=off GOPROXY=off go build ./...

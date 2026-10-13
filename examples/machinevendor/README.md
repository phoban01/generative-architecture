# Machine-vendor example

The original domain-specific Go implementation is isolated here. Existing root `system.yaml`, contracts, runtime/behaviour YAML, reports and CLI commands remain its entrypoints.

Shared CEL compilation, Go structural extraction, API interception, real worker scheduling and queue-model transformations come from `pkg/`. The branch/assignment extractor now delegates to `pkg/goflow`; domain effect summaries, simulator and projection adapters remain example-specific. Start with `../../START-HERE.md` before exploring this larger integration.

Run `make verify` at the repository root. See `../../LIBRARY.md` for the public API boundary and `../../COMPOSITION.md` for this example's source-derived behaviour.

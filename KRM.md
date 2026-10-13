# Architecture documents as KRM resources

All authored YAML documents now have `apiVersion`, `kind`, `metadata.name` and `spec`. Generated observations have `status`. These are local Kubernetes Resource Model documents consumed by the Go tools; they do not require a running cluster. No architecture CRDs are installed or generated yet.

```yaml
apiVersion: architecture.local/v1alpha1
kind: Contract
metadata:
  name: mac
spec:
  version: 0.3.0
  title: Machine allocation controller (MAC)
  # ports, state and actions follow here, as in contracts/mac.yaml.
```

`metadata.name` is the contract identity; there is no duplicate `spec.id`. `apiVersion` versions the document format. `spec.version` independently versions the behavioral contract. Contract bodies retain CEL strings and the existing state/action definitions. This migration does not add the proposed public `implements` conformance marker: the current Contract still describes the component's behavioral model.

| Document | Kind | Payload |
| --- | --- | --- |
| `contracts/*.yaml` | Contract | spec |
| `system.yaml` | System | spec |
| `source-mappings.yaml` | SourceBindings | spec |
| `scenarios.yaml` | ScenarioSuite | spec |
| `behavior/publication.yaml` | BehaviorAnalysis | spec |
| `behavior/composition.yaml` | ProcessComposition | spec |
| `runtime/controller-runtime.yaml` | RuntimeProfile | spec |
| `contracts.lock.yaml` | ContractLock | spec |
| `generated/source-descriptors.yaml` | SourceDescriptor | status |
| `generated/architecture.yaml` | Architecture | status |
| `generated/mbt-bindings.yaml` | TestBindings | status |
| `generated/behavior/*.yaml` | BehaviorProgram | status |
| `generated/composition/composition.yaml` | ComposedProgram | status |
| `traces/manifest.yaml` | TraceManifest | status |
| `reports/**/summary.yaml` and other result reports | VerificationReport | status |
| Readable behavior counterexamples | Counterexample | status |

The Go loader validates the expected kind, supported API version, required payload, names, labels and annotation keys. It rejects unknown typed fields, duplicate keys, aliases, multiple YAML documents and malformed CEL scalars. This prototype uses cluster-scoped local identities, so nonempty namespaces are rejected explicitly. Payload maps such as scenario names remain intentionally extensible. The loader is not a general Kubernetes admission/schema implementation.

Files remain the composition unit: `System.spec.components` selects contract paths and versions; `System.spec.sources` selects source extraction; `System.spec.runtime` selects the runtime profile; `System.spec.composition` selects the process/runtime composition. Bindings remain explicit, checked semantic joins. KRM supplies stable identities and document structure, not automatic discovery of semantic relationships. Kustomize/kpt-style editing is possible at the YAML level, but there is no ResourceList, function pipeline or Kustomize integration test in this prototype.

`pkg/krm` is shared by source extraction, model generation and runtime conformance. ITF traces stay in the upstream JSON interchange format; Quint and LikeC4 outputs retain their own languages.

Run `go run ./cmd/arch generate` after source edits. Contract changes require a reviewed lock refresh with `--update-lock`. Old bare YAML inputs are rejected instead of silently being treated as the new format.

Conventions: https://kubernetes.io/docs/concepts/overview/working-with-objects/ and https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/.

## Public library model

The library adds `TransitionModel` (`spec`: `model.Spec`) for domain-neutral typed state, effect identities, guarded CEL updates and properties. See `examples/configmirror/system.yaml` and `LIBRARY.md`. Existing machine-vendor `System` and `Contract` payloads retain their example-specific schema; they are not interchangeable with `TransitionModel`.

# Architecture documents as KRM resources

All authored YAML documents have `apiVersion`, `kind`, `metadata.name` and `spec`. Generated observations have `status`. These are local Kubernetes Resource Model documents consumed by the Go tools; they do not require a running cluster. No architecture CRDs are installed or generated yet.

```yaml
apiVersion: architecture.local/v1alpha1
kind: TransitionModel
metadata:
  name: database
spec:
  name: DatabaseWalkthrough
  state:
    databaseExists: {type: bool, initial: 'false'}
    instanceEndpoint: {type: string, initial: '""'}
  effects:
    - id: readDatabase
      resource: {group: demo.architecture.local, version: v1alpha1, kind: Database}
      operation: get
      consistency: direct
  actions:
    - name: provisionInstance
      guard: 'instanceExists && instanceEndpoint == ""'
      updates: {instanceEndpoint: '"db-1.internal"'}
```

`metadata.name` is the document identity. `apiVersion` versions the document format. Bodies retain CEL strings and the typed state/action definitions described in [CEL.md](CEL.md).

| Document | Kind | Payload | Used by |
| --- | --- | --- | --- |
| `examples/refsystem/project.yaml` | Project | spec | `pkg/project`: module, package patterns, catalog sources, constraints, boundaries, runtime profile and bounds; referenced paths are checked and hashed (ADOPT-1) |
| `examples/database/model.yaml`, `examples/configmirror/system.yaml` | TransitionModel | spec | `pkg/model`, `pkg/process`, `pkg/composition` |
| `examples/database/process.yaml` | ProcessSemantics | spec | `pkg/process` (effect summaries for extracted Go calls) |
| `examples/database/resources.yaml` | KubernetesResourceCatalog | spec | `pkg/dynamicgen` (GVR/GVK/plural/scope bindings) |
| `examples/database/constraints.yaml` | ModelConstraints | spec | independent properties compiled apart from the behaviour model |
| An abstraction document (`pkg/abstraction/testdata/database.yaml`) | Abstraction | spec | `pkg/abstraction`: bounded objects, fields, value domains, references and locals that derive model state (ADR-0005) |
| A mutation suite document | MutationSuite | spec | `pkg/mutation`: named find/replace mutations of a file with the constraints expected to detect them; `Run` builds the detection matrix in memory (CON-8) |
| `pkg/summaries/data/*.yaml` (embedded) | EffectSummaries | spec | `pkg/summaries`: built-in outcomes of client operations as CEL templates over `$placeholders` (ARCH-7) |
| A generated IR document | ArchitectureIR | status | `pkg/ir.Document`: controllers, watches, effects (kind `api`, or `external` with a callee target and declared outcomes), processes, contracts and constraints with evidence and provenance |
| `examples/runtimegroup/profile.yaml` | RuntimeGroup | spec | `pkg/controllerruntime.BuildGroupModel` |
| A system composition document | SystemComposition | spec | `pkg/composition.Load` |
| An interaction contract document | InteractionContract | spec | `pkg/contracts` (`+arch:implements` checks) |
| `examples/database/generated/process.yaml` | ExtractedProcess | status | control-flow graph and source evidence |
| `examples/database/generated/watches.yaml` | ExtractedWatches | status | registrations, mapper field paths, predicates, hashes |
| `examples/database/generated/dynamic-client.yaml` | ExtractedDynamicAccess | status | dynamic-client methods, resources, verbs, hashes |
| `examples/*/report.yaml` | VerificationReport | status | trace counts, coverage, input and trace hashes |
| An evidence receipt document | EvidenceReceipt | status | `pkg/receipt`: the consumed input closure (selected packages, their in-module transitive imports, go.mod/go.sum, extra documents) with SHA-256 per file and exact tool versions; `Verify` reports changed, missing and added files (EVID-5) |

The Go loader (`pkg/krm`) validates the expected kind, supported API version, required payload, names, labels and annotation keys. It rejects unknown typed fields, duplicate keys, aliases, multiple YAML documents and malformed CEL scalars. Documents use cluster-scoped local identities, so nonempty namespaces are rejected explicitly. Payload maps such as state names remain intentionally extensible. The loader is not a general Kubernetes admission or schema implementation.

Files remain the composition unit. A `TransitionModel` is self-contained; `ProcessSemantics`, `KubernetesResourceCatalog` and `ModelConstraints` are joined to it by the example's loader, and `SystemComposition` references models in local project checkouts. Bindings are explicit, checked semantic joins. KRM supplies stable identities and document structure, not automatic discovery of semantic relationships. Kustomize/kpt-style editing is possible at the YAML level, but there is no ResourceList, function pipeline or Kustomize integration test.

`pkg/krm` is shared by source extraction, model generation and runtime conformance. ITF traces stay in the upstream JSON interchange format; Quint and LikeC4 outputs retain their own languages. Old bare YAML inputs are rejected instead of silently being treated as the new format.

Conventions: https://kubernetes.io/docs/concepts/overview/working-with-objects/ and https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/.

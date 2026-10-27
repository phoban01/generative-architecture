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
| `examples/refsystem/project.yaml`, `examples/configmirror/project.yaml` | Project | spec | `pkg/project`: module, package patterns, catalog sources, constraints, boundaries, runtime profile and bounds; referenced paths are checked and hashed (ADOPT-1) |
| `examples/database/generated/model.yaml`, `examples/configmirror/generated/model.yaml` (generated) | TransitionModel | spec | `pkg/model`, `pkg/process`, `pkg/composition` |
| `examples/database/process.yaml` | ProcessSemantics | spec | `pkg/process` (effect summaries for extracted Go calls) |
| `examples/database/resources.yaml` | KubernetesResourceCatalog | spec | `pkg/dynamicgen` (GVR/GVK/plural/scope bindings) |
| `examples/database/constraints.yaml`, `examples/configmirror/constraints.yaml` | ModelConstraints | spec | independent properties compiled apart from the behaviour model |
| `examples/configmirror/abstraction.yaml`, `pkg/abstraction/testdata/database.yaml` | Abstraction | spec | `pkg/abstraction`: bounded objects, fields, value domains, references and locals that derive model state (ADR-0005) |
| `examples/configmirror/mutations.yaml` | MutationSuite | spec | `pkg/mutation`: named find/replace mutations of a file with the constraints expected to detect them; `Run` builds the detection matrix in memory (CON-8) |
| `pkg/summaries/data/*.yaml` (embedded) | EffectSummaries | spec | `pkg/summaries`: built-in outcomes of client operations as CEL templates over `$placeholders` (ARCH-7) |
| An external effects document (`pkg/goflow/testdata/external/effects.yaml`) | ExternalEffects | spec | `pkg/goflow`: declared outcomes of calls outside the API, keyed by callee symbol; typed extraction lowers a matching call to an `effect` node and `pkg/process` expands the outcomes (ARCH-13) |
| A generated IR document | ArchitectureIR | status | `pkg/ir.Document`: controllers, watches, effects (kind `api`, or `external` with a callee target and declared outcomes), processes, contracts and constraints with evidence and provenance |
| `examples/runtimegroup/profile.yaml` | RuntimeGroup | spec | `pkg/controllerruntime.BuildGroupModel` |
| A system composition document | SystemComposition | spec | `pkg/composition.Load`: each component names a TransitionModel with declared ports, or a BoundaryModel whose ports, watches and effects enter the composition with `assumed` evidence |
| A protocol contract named by a composition link | ProtocolContract | spec | `pkg/protocol`: roles, states, permitted writes to the bound state and properties; `pkg/composition` generates a conformance monitor and the properties into the composed model (CON-10) |
| A boundary model of an upstream project | BoundaryModel | spec | `pkg/boundary`: pinned upstream version and optional image digest, owned and watched resources, effects, ports, contracts and assumptions; each assumption is `declared` or `assumed` and names a conformance hook (`envtest`, `documentation` or `none`) (EXT-1) |
| `pkg/boundary/library/flux/kustomization.yaml`, `kustomization-model.yaml` | BoundaryModel, TransitionModel | spec | Flux kustomize-controller v1.4.0: `interval` as a periodic requeue, `dependsOn` as an ordering guard, server-side apply and prune as declared assumptions (EXT-4) |
| `pkg/boundary/library/flux/gitrepository.yaml`, `gitrepository-model.yaml` | BoundaryModel, TransitionModel | spec | Flux source-controller v1.4.1: the artifact revision advances on fetch after `interval`; Ready follows the stored artifact (EXT-4) |
| `pkg/boundary/library/flux/helmrelease.yaml`, `helmrelease-model.yaml` | BoundaryModel, TransitionModel | spec | Flux helm-controller v1.1.0: install, then upgrade on a chart change, after `interval` and `dependsOn`; release storage Secrets as effects (EXT-4) |
| `pkg/boundary/library/clusterapi/clustercache.yaml`, `clustercache-model.yaml` | BoundaryModel, TransitionModel | spec | Cluster API v1.9.0 cluster cache: runs in cluster `management` and reads Nodes in `workload_a` and `workload_b` as assumed cross-cluster facts; per-cluster connect, sync and connection loss; consumers bind a workload cluster with an explicit link (EXT-3, ARCH-3) |
| An interaction contract document | InteractionContract | spec | `pkg/contracts` (`+arch:implements` checks) |
| `examples/database/generated/process.yaml` | ExtractedProcess | status | control-flow graph and source evidence |
| `examples/database/generated/watches.yaml` | ExtractedWatches | status | registrations, mapper field paths, predicates, hashes |
| `examples/database/generated/dynamic-client.yaml` | ExtractedDynamicAccess | status | dynamic-client methods, resources, verbs, hashes |
| `examples/*/report.yaml` | VerificationReport | status | trace counts, coverage, input and trace hashes |
| `examples/configmirror/receipt.yaml` | EvidenceReceipt | status | `pkg/receipt`: the consumed input closure (selected packages, their in-module transitive imports and embedded files, go.mod/go.sum, extra documents) with SHA-256 per file, exact tool versions and the abstraction's bounds and excludes; `Verify` reports changed, missing and added files (EVID-5) |

The Go loader (`pkg/krm`) validates the expected kind, supported API version, required payload, names, labels and annotation keys. It rejects unknown typed fields, duplicate keys, aliases, multiple YAML documents and malformed CEL scalars. Documents use cluster-scoped local identities, so nonempty namespaces are rejected explicitly. Payload maps such as state names remain intentionally extensible. The loader is not a general Kubernetes admission or schema implementation.

Abstraction paths (`fields`, `identity` and `references`) are lists of identifiers. The last element of a path with two or more elements may be a map key instead. Write a map key as a double-quoted string inside the YAML string, with Go escapes: `path: [data, '"app.config/key"']`. The key may hold any non-empty string, such as dots and slashes. A field whose path holds a map key must declare `name`, because the key cannot form a variable name. The key after `metadata.annotations` or `metadata.labels` may also be unquoted, as before. Any other element that is not an identifier fails validation. The error names the resource, the list entry and the element, such as `fields[1]: path[1]`. Generators read the unquoted key (`abstraction.JSONPath`).

Files remain the composition unit. A `TransitionModel` is self-contained; `ProcessSemantics`, `KubernetesResourceCatalog` and `ModelConstraints` are joined to it by the example's loader, and `SystemComposition` references models in local project checkouts. Bindings are explicit, checked semantic joins. KRM supplies stable identities and document structure, not automatic discovery of semantic relationships. Kustomize/kpt-style editing is possible at the YAML level, but there is no ResourceList, function pipeline or Kustomize integration test.

`pkg/krm` is shared by source extraction, model generation and runtime conformance. ITF traces stay in the upstream JSON interchange format; Quint and LikeC4 outputs retain their own languages. Old bare YAML inputs are rejected instead of silently being treated as the new format.

Conventions: https://kubernetes.io/docs/concepts/overview/working-with-objects/ and https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/.

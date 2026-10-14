# Built-in effect summaries

Each `*.yaml` file in this directory is one `EffectSummaries` document
embedded into `pkg/summaries`. A summary names an operation (`get`, `list`,
`create`, `update`, `patch`, `delete`, `deleteAllOf`, `apply`, or
`<subresource>.<operation>` such as `status.update`), a read consistency
(`cached` or `direct`) and its outcomes. Each outcome has a CEL guard, CEL
updates and an optional error class (`notFound`, `alreadyExists`, `conflict`,
`transient`). An outcome without an error class is a success.

Guards and updates are templates over placeholders, written `$name`. A
summary lists every placeholder it uses under `bindings`; the process
lowering binds each one to a model expression (or, for update targets, a
model variable) before the outcome becomes an action. Substitution is
textual, so a binding is any CEL expression.

## Placeholders

| Placeholder | Meaning |
| --- | --- |
| `$exists` | Whether the addressed object exists where the operation reads it: the API server for `direct`, the informer cache for `cached` |
| `$object` | The object stored by the API server (or the selected fields of it) |
| `$resourceVersion` | The stored object's version, modelled as a counter that every write increments |
| `$cache` | The informer cache's copy of the object, which may lag `$object` |
| `$cacheVersion` | The version of the cached copy |
| `$value` | The object the caller passes in, as extracted from the Go call |
| `$snapshot` | The caller's local copy of the object, written by reads |
| `$snapshotVersion` | The version the caller's copy was read at; writes compare it with `$resourceVersion` |

Every outcome has a guard of its own, so one call can have several enabled
outcomes; the `error` outcome (guard `true`, class `transient`) is always
enabled and models the API being unavailable. A caller that needs no
transient failures excludes that outcome when it expands the call.

## Reads

`get` at `direct` consistency reads `$object` and `$resourceVersion`; at
`cached` consistency it reads `$cache` and `$cacheVersion` instead, and
`$exists` is bound to the cache's view of the object. Both record the copy
and its version in `$snapshot` and `$snapshotVersion`.

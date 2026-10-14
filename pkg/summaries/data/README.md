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

| Placeholder | Meaning |
| --- | --- |
| `$exists` | Whether the addressed object exists where the operation reads it |
| `$object` | The stored object (or the selected fields of it) |
| `$value` | The object the caller passes in, as extracted from the Go call |
| `$snapshot` | The caller's local copy of the object, written by reads |

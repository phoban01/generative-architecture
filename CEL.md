# Authoring in YAML and CEL

YAML describes typed state, effects and transitions. CEL supplies read-only expressions for initial values, guards, update values and properties. `pkg/expr` type-checks a deliberately bounded CEL subset and lowers it to Quint; the authored files contain no Quint syntax or `{{...}}` substitutions.

For example, the sample controller's environment action that provisions an instance, from the generated `examples/database/generated/model.yaml`, is:

```yaml
- name: changeInst1_1
  guard: DatabaseInstance_inst1_exists && DatabaseInstance_inst1_status_endpoint == ""
  updates: {DatabaseInstance_inst1_status_endpoint: '"db-1.internal"'}
```

The controller's own steps are not authored this way. `pkg/goflow` extracts branches, calls and returns from `Reconcile`, `pkg/process` lowers them into actions using the effect summaries in `process.yaml`, and the result is the same kind of action.

An independent requirement, from `examples/database/constraints.yaml`, uses the same state names:

```yaml
- name: publicationRequiresProvisionedInstance
  expression: 'Database_db1_status_endpoint == "" || (DatabaseInstance_inst1_exists && DatabaseInstance_inst1_status_endpoint != "" && Database_db1_status_endpoint == DatabaseInstance_inst1_status_endpoint)'
```

Properties are compiled separately from the behaviour model. A trace can therefore conform to the implementation while violating a requirement, which is what the source-mutation tests demonstrate.

## Updates are simultaneous

CEL does not mutate state. Each `updates` entry maps a state variable to a CEL expression evaluated against the **pre-transition state**, so multiple writes in one action are simultaneous, and untouched state is held unchanged. The action `guard` controls whether the action is enabled. Composed models additionally bind input ports to a provider's state by rewriting symbols on the CEL syntax tree (`expr.Rewrite`), so renaming a variable never rewrites a string literal.

## Supported subset

The parser is `cel.dev/cel-go`, pinned in `go.mod`. The compiler uses its parsed AST, not text replacement, and statically checks the subset below before emitting Quint:

- String, boolean and signed integer literals.
- Declared identifiers: state variables, bound ports, action locals and the `event` name inside properties.
- Field access on declared record types (`records:` in a `TransitionModel`).
- String-keyed map indexing on `map(string, T)` state.
- Boolean operators `&&`, `||`, `!`.
- Equality and inequality on strings, integers, booleans and declared records.
- Integer addition, subtraction, unary minus and comparisons.
- Conditional expressions: `condition ? valueA : valueB`.
- `DOMAIN.all(x, predicate)` over a declared string domain, including nested quantifiers.

State types are `bool`, `int`, `string`, declared record names and `map(string, T)` over those. Unsupported expressions fail compilation with a message naming the construct. This includes `has()`, `exists()`, `filter()`, `map()`, receiver functions, regexes, message literals, list literals, optional access, doubles and unsigned integers. Supporting more CEL requires adding and testing a sound translation, rather than passing expressions through to Quint.

CEL has int64 semantics while Quint has mathematical integers. Models keep integer state within small declared bounds and are checked using exact int64 values in CEL. The compiler does **not** prove absence of overflow or missing-key errors for arbitrary new models. CEL's error-aware boolean semantics are not modelled as separate Quint values. New expressions must stay within the declared, total finite domains; simulation and differential checks provide evidence for exercised states, not a universal proof of equivalence.

## Checking the translation

`go run ./cmd/walkthrough --refresh` performs three independent comparisons on the sample controller:

1. **Quint simulation:** Quint samples bounded traces of the generated model and checks the generated properties.
2. **CEL ↔ Quint:** Go evaluates the original YAML CEL expressions with cel-go on every state of every trace. This catches translation errors without evaluating the generated Quint text in the CEL runtime. `quint.Differential` runs the same comparison for any compiled `TransitionModel` and its ITF traces, counting transitions and reporting the first disagreement as a `tool` failure.
3. **Model ↔ implementation:** Go replays each trace against the real `Reconcile` running on a fake dynamic API, pausing at every observable effect boundary. The adapter never reads expected state.

The saved `report.yaml` records the model hash, input hashes and trace hashes, so native tests detect stale evidence without Quint. This is sampled differential testing, not full verification of a general CEL-to-Quint compiler.

References: [CEL language specification](https://github.com/cel-expr/cel-spec/blob/master/doc/langdef.md), [cel-go parser and evaluator](https://github.com/cel-expr/cel-go).

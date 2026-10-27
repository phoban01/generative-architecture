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
- `DOMAIN.all(x, predicate)` and `DOMAIN.exists(x, predicate)` over a declared string domain, including nested quantifiers.

State types are `bool`, `int`, `string`, declared record names and `map(string, T)` over those. Unsupported expressions fail compilation with a message that names the construct and its line and column. This includes `has()`, `exists_one()`, `filter()`, `map()`, receiver functions, regexes, message literals, list literals, optional access, doubles and unsigned integers. Supporting more CEL requires adding and testing a sound translation, rather than passing expressions through to Quint.

### Quantifiers over declared domains

A `TransitionModel` declares its finite domains under `domains:`. Each domain is a named, non-empty list of distinct strings. It is a constant, not state:

```yaml
domains:
  Keys: [a, b]
properties:
  - name: someKeyReady
    expression: 'Keys.exists(k, (k == "a" && aReady) || (k == "b" && bReady))'
```

`Keys.exists(k, p)` lowers to `Keys.exists(k => p)` and `Keys.all(k, p)` lowers to `Keys.forall(k => p)`, where Quint's `Keys` is `Set("a", "b")`. Over a finite, declared domain both sides quantify over the same values, so the lowering is sound. A quantifier over anything else fails with a located message: an undeclared name (`exists() ranges over Pods, which is not a declared domain`), map state, a string, or a list literal. The binder must not shadow a declared name. `exists_one()` and the two-variable forms are not supported.

`TestQuantifierTruthTable` in `pkg/expr` compares cel-go with the truth table of the lowering on every state of a two-element domain. `TestQuantifierDifferentialOverSampledTraces` in `pkg/quint` samples a model with Quint, stores each quantifier's Quint value in state, and recomputes it with cel-go through `quint.Differential`. It also requires that each quantifier is both true and false somewhere in the sample.

CEL has int64 semantics while Quint has mathematical integers. Models keep integer state within small declared bounds and are checked using exact int64 values in CEL. The compiler does **not** prove absence of overflow or missing-key errors for arbitrary new models. CEL's error-aware boolean semantics are not modelled as separate Quint values. New expressions must stay within the declared, total finite domains; simulation and differential checks provide evidence for exercised states, not a universal proof of equivalence.

## Temporal properties

Properties hold on every state. A `ModelConstraints` document can also declare liveness properties under `temporal:`, with the fairness they assume under `fairness:`. Each property sets exactly one of `eventually` (a CEL state predicate) and `leadsTo` (a `from` and a `to` predicate). Predicates use the same CEL subset and names as properties. Fairness lists action names: a `weak` action that stays enabled is eventually taken, and a `strong` action that is enabled infinitely often is eventually taken.

```yaml
temporal:
  - name: queueDrains
    leadsTo: {from: 'ready', to: '!ready'}
  - name: copyCatchesUp
    eventually: 'ConfigMap_copy_data_value == snapshot'
fairness:
  weak: [begin, finish]
```

`model.Compiled.TemporalModule` adds each property to the model as a Quint `temporal` definition: `(weakFair(a, vars) and strongFair(b, vars)) implies eventually(p)`, or `implies always(from implies eventually(to))` for `leadsTo`. `vars` is the tuple of every state variable and `event`. An unknown or repeated fair action, a name clash or a predicate outside the subset fails with the file and the line and column of the predicate.

`arch check` gives each temporal property a status in the report's `temporal:` section:

- `verified` or `violated`: only with `--verify`, after `quint verify --backend tlc` completed under the declared fairness. Each entry records the fairness, the Quint version, the backend and the bound. A violation is also a `safety` finding with the start of the counterexample.
- `unchecked`: in every other case, with a reason, and as an `unsupported` finding. This covers a run without `--verify`, a document without fairness, a stale model and a checker that could not finish.

Sampling never checks a temporal property, so a report never claims liveness from simulation. The checker is TLC because Quint 0.33 marks temporal support in Apalache as experimental. TLC needs a Java 21 runtime. It has no depth bound: it searches the complete reachable state space, so it completes only on a finite and small state space. Each run has a time limit of five minutes and a Java heap cap of 2 GiB (`quint.VerifyHeap`). A run that hits either limit leaves the property unchecked. On ConfigMirror with the example above, TLC finds a counterexample for `queueDrains` in under three minutes: the environment actions are not fair, so the model may stutter while `ready` holds. `copyCatchesUp` reaches the time limit and stays unchecked. A violation finding lists the events of the counterexample and its last state, at most 1500 characters.

The tests in `pkg/quint/verify_test.go` and `cmd/arch/temporal_test.go` run TLC on a small counter model, with a property that holds and one that is violated. They skip when `QUINT_BIN` is unset or `java` is not on `PATH`.

## Checking the translation

`go run ./cmd/walkthrough --refresh` performs three independent comparisons on the sample controller:

1. **Quint simulation:** Quint samples bounded traces of the generated model and checks the generated properties.
2. **CEL ↔ Quint:** Go evaluates the original YAML CEL expressions with cel-go on every state of every trace. This catches translation errors without evaluating the generated Quint text in the CEL runtime. `quint.Differential` runs the same comparison for any compiled `TransitionModel` and its ITF traces, counting transitions and reporting the first disagreement as a `tool` failure.
3. **Model ↔ implementation:** Go replays each trace against the real `Reconcile` running on a fake dynamic API, pausing at every observable effect boundary. The adapter never reads expected state.

The saved `report.yaml` records the model hash, input hashes and trace hashes, so native tests detect stale evidence without Quint. This is sampled differential testing, not full verification of a general CEL-to-Quint compiler.

References: [CEL language specification](https://github.com/cel-expr/cel-spec/blob/master/doc/langdef.md), [cel-go parser and evaluator](https://github.com/cel-expr/cel-go).

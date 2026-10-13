# Authoring in YAML and CEL

YAML describes state, ports, bindings and transitions. CEL supplies read-only expressions for initial values, guards, local values, update keys/values/conditions, acceptance results and system properties. The compiler translates this representation into Quint; the authored files contain no Quint syntax or `{{...}}` substitutions.

For example, the MAC publication action in `contracts/mac.yaml` is:

```yaml
- name: publish
  params: [infra]
  guard: claims[infra]
  locals:
    allowed: >-
      observed[infra].machineId != "" &&
      observed[infra] == lease[infra]
  updates:
    assigned:
      key: infra
      value: observed[infra].machineId
      when: allowed
  accepted: allowed
```

`lease` is the bound input port. `observed`, `claims` and `assigned` are MAC-owned state. `infra` is the action parameter. `allowed` is a local CEL value. The infrastructure controller sees only the resulting machine ID through `mac.assignment → infra.assignment`.

An invariant uses qualified component state names:

```yaml
- name: publicationRequiresCurrentLease
  expression: >-
    event.name != "mac.publish" || !event.accepted ||
    (mac.assigned[event.infra] == claim.leases[event.infra].machineId &&
     claim.leases[event.infra].machineId != "")
```

Component expressions can reference their own state and declared ports. They cannot directly reference other components' state. System properties may use qualified names to express cross-component requirements.

## Updates are YAML operations, not CEL functions

CEL does not mutate state. Each `updates` entry is an explicit map update:

```yaml
updates:
  epochs:
    key: host
    value: epochs[host] + 1
    when: epochs[host] < MAX_EPOCH
```

The compiler emits a Quint map update and holds all untouched state unchanged. All keys, values and conditions are evaluated against the **pre-transition state**, so multiple writes are simultaneous. `when: false` must be written as `when: "false"` when it is intended as a CEL expression.

The action guard controls whether the action is enabled. `when` controls whether that particular update happens. `accepted` records the operation's observable result; it is not implicitly a guard. Local definitions are evaluated in declaration order and cannot shadow inputs, state, parameters or constants.

State initialization also uses YAML structure:

```yaml
state:
  leases:
    type: map(string, Assignment)
    init:
      domain: INFRAS
      value: EMPTY_ASSIGNMENT
```

This creates one map entry per finite domain member. Values are CEL expressions over constants. Domains are currently `HOSTS` and `INFRAS`. Their CEL runtime representations are lists of distinct identities; the Quint model uses sets. The supported `all()` operation does not depend on order.

## Supported subset

The parser is `cel.dev/cel-go`, pinned to v0.32.0. The Go compiler uses its parsed AST, not text replacement, and statically checks the subset below before emitting Quint:

- String, boolean and signed integer literals.
- Declared identifiers and fixed record field access.
- String-keyed map indexing, such as `lease[infra]`.
- Boolean operators `&&`, `||`, `!`.
- Equality/inequality on strings, integers, booleans and Assignment values.
- Integer addition/subtraction and comparisons.
- Conditional expressions: `condition ? valueA : valueB`.
- `HOSTS.all(h, predicate)` and `INFRAS.all(i, predicate)`, including nested quantifiers.
- Assignment map literals with exactly `machineId: string` and `epoch: int`, for example `{"machineId": host, "epoch": epochs[host] + 1}`. These become fixed Quint records, not general dynamic maps.

State types are `map(string, bool)`, `map(string, int)`, `map(string, string)` and `map(string, Assignment)`. `Assignment` and the trace `Event` record are predefined prototype types.

Unsupported expressions fail compilation. This includes `exists()`, `filter()`, `map()`, `has()`, arbitrary receiver functions, regexes, protobuf literals, list literals, optional access, doubles, uints and general dynamic map literals. Supporting more CEL requires adding and testing a sound translation, rather than passing expressions through to Quint.

CEL has int64 semantics while Quint has mathematical integers. This finite example keeps epochs between zero and three and is checked using exact int64 values in CEL. The compiler does **not** prove absence of overflow or missing-key errors for arbitrary new contracts. Similarly, CEL's error-aware boolean semantics are not modelled as separate Quint values. New expressions must stay within the declared, total finite domains; simulation and differential checks provide evidence for exercised states, not a universal proof of equivalence.

## Checking the translation

`go run ./cmd/arch check` performs two independent comparisons:

1. **CEL ↔ Quint:** Evaluate the original YAML CEL expressions with cel-go and compare initialization, every transition, acceptance results, invariants and the safety counterexample against actual Quint ITF traces. This catches translation errors without evaluating the generated Quint text in the CEL runtime.
2. **Quint ↔ simulator:** Replay those traces against the separately written implementation. The simulator does not read the CEL expressions or expected state. Deliberately faulty MAC behavior must fail conformance.

For the current run, 204 valid traces cover 8,039 transitions. Including the counterexample, direct CEL evaluation performs 61,449 expression evaluations. This is sampled differential testing, not full verification of a general CEL-to-Quint compiler.

The known revocation race remains present: YAML/CEL changes the authoring layer, not the system's authorization semantics.

References: [CEL language specification](https://github.com/cel-expr/cel-spec/blob/master/doc/langdef.md), [cel-go parser and evaluator](https://github.com/cel-expr/cel-go).

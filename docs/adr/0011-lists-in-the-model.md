# ADR-0011: Lists in the model

Status: accepted (2026-11-26)

## Context

A reconciler that lists objects iterates over the result: the kubebuilder
CronJob tutorial lists its Jobs, sorts them into active, failed and
successful lists, counts them and acts on each. The model had no collection
a controller could hold. Its only collections were the declared domains,
which are constants, and the summary of `List` bound `$items` to a filter
over `$catalog` that nothing could compile. Gaps 9, 10, 12 and 18 of the
CronJob follow-ups all rest on this.

Two shapes were possible. The first unrolls every loop over the catalog's
population at generation time, so each iteration sees one declared object
and a local slice becomes one counter per object. It fixes the object set
at generation time and makes `append` and `len` approximate. The second
gives the model a real list type. Quint holds lists, and the summaries,
the environment and the projection have to learn them.

## Decision

The model gets a list type, and lists hold object identities.

1. `list(string)` is a model type beside `string`, `bool`, `int`, the
   records and the map types. It is an ordered list of strings and lowers
   to Quint's `List[str]`. Its CEL operations are the list literal,
   `size()`, indexing, `+` as concatenation, `in` as membership and the
   `all()` and `exists()` quantifiers over its elements. A list variable
   starts at `[]` unless the project says otherwise.
2. A list in a controller's model holds abstraction object IDs, never
   objects. The objects' fields stay the per-object variables the
   abstraction derives, so nothing changes for a model without lists.
3. The summary of `List` binds `$items` to a `list(string)` local. The
   generator lowers `$catalog` to the population of the listed resource
   and the summary's filter to the per-object variables the filter reads,
   so `$items` is the list of the IDs of the objects that exist and match
   the call's options, in population order.
4. A `range` over a list local lowers to a loop in the process graph: an
   index local the frontend introduces, a head that tests the index against
   the list's size, the body, and an increment that returns to the head.
   The element local takes the element at the index. `continue` jumps to
   the increment and `break` to the exit. The loop is bounded because
   every list is a sublist of a population, which `maxObjectsPerResource`
   bounds.
5. A field selection rooted at an element local lowers to a choice over
   the population: `job.Status.Succeeded` becomes the variable of each
   object guarded by `job == "<id>"`, as a concatenation with a field
   lowers over its domain (#170). A write through an element local updates
   each object's variable under the same guard. A client call whose object
   is an element local binds the summary's placeholders the same way.
6. `len(xs)` lowers to `size(xs)` and `append(xs, e)` to `xs + [e]`, where
   `e` is an element local or an indexed element of a list local. The
   abstraction declares list locals as `locals` of type `list(string)`.
7. Replay observes lists like other process locals: it does not project
   them. The environment never creates a list, since lists are controller
   state. A later increment may project a list of IDs from the API when a
   constraint needs it.

## Consequences

- The expression compiler and the model compiler gain the list type first,
  with a Quint differential test. The frontend's loop lowering, the List
  summary binding and the element-local choices follow as their own
  increments, each closing a CronJob follow-up: range loops (#160), `len`
  (#144), then the non-scalar fields (#146, #152).
- A list element is an identity string, so a loop body that reads a field
  of the element compiles to one branch per declared object. Models with
  large populations grow with them; the bound is the abstraction's.
- Quint has `List[str]` but no string operations, so a list never holds
  derived strings: the concatenation lowering of #170 produces constants
  per input value and stays compatible.

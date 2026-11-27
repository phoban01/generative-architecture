# ADR-0012: Time is a declared choice

Status: accepted (2026-11-27)

## Context

Controllers read the clock. The CronJob tutorial parses a schedule, finds
the run it missed, tests whether that run is too late and requeues until
the next one. Quint has no time type, the model has no clock a controller
can read, and the frontend does not lower time values: `missedRun.IsZero()`
and `tooLate = missedRun.Add(...).Before(r.Now())` are gaps 11 of the
CronJob follow-ups, and `r.Now()` inside a log statement blocks the
statement, since dropping the log would drop the call.

The runtime model already has a clock: ticks drive the queue's timers, and
a requeue after a delay is a timer, not a time value (#158). What the
reconcile logic needs from time is a decision: is a run due, is it too
late, has a deadline passed.

## Decision

The model does not compute with time. Time values stay opaque, and every
decision a controller takes on time is a choice the environment makes.

1. The author declares the decision as an abstraction local and binds the
   Go expression that computes it to the local, under `choices` of the
   controller declaration: the expression's text, as `gofmt` prints it,
   maps to the local's name. The frontend lowers the expression to the
   local and reads nothing inside it.
2. The environment chooses the local's value: one environment action per
   value of the local's type sets it, so the model explores every outcome
   of the decision in every state. A `bool` local has its two values; a
   `string` or `int` local declares its `values` in the abstraction.
3. A call that only reads the clock, such as `r.Now()` through an embedded
   `Clock` interface, is pure when the controller declaration lists it
   under `pure`. A pure call inside a log statement is dropped with the
   statement, and a statement that only calls one has no effect. A pure
   call used as a value still needs a choice.
4. The replay adapter does not replay a controller with choices yet: the
   implementation reads a real clock, which the model does not constrain.
   `arch generate` says so and generates no adapter. A later increment may
   inject a clock through the controller's `Clock` field and derive its
   value from the chosen decisions.

## Consequences

- A time-dependent branch becomes a nondeterministic branch. The model
  over-approximates the implementation: it also explores orders the real
  clock would not produce. Properties that hold are sound; a property that
  depends on time ordering needs the clock injection of point 4.
- Schedule computation stays out. A closure that parses a cron expression
  and returns times extracts nothing useful, and its result locals need
  the author to declare the decisions that depend on them.
- `choices` and `pure` are the first controller declarations that name Go
  source text. The support report lists an expression a choice names that
  the body does not contain, so a stale binding is visible.

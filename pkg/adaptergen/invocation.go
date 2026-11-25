package adaptergen

import (
	"fmt"
	"sort"
)

// InvocationBinding describes the invocation driving of a controller whose
// model composes a watch queue instead of the runtime group model: the
// events that begin an invocation or leave the controller idle, and the
// model variables that observe invocations.
type InvocationBinding struct {
	// Reconcile is the event that begins an invocation; Idle the event
	// that leaves the controller idle.
	Reconcile, Idle string
	// Counter is the model variable counting invocations; Outcome the
	// variable holding the controller's result, as the process reports it.
	Counter, Outcome string
	// Locals names, per effect, the Go locals the controller assigns the
	// effect's results to, in result order; "" skips a result. Each
	// local's model type (string, bool or int) is in Types, so a new
	// invocation sees the zero value.
	Locals map[string][]string
	Types  map[string]string
	// Counters names, per effect, a model variable that counts the
	// effect's calls across invocations.
	Counters map[string]string
}

type invocationLocal struct {
	Name, Zero string
}

type invocationResult struct {
	Effect string
	Index  int
	Name   string
}

type invocationData struct {
	Package          string
	Reconcile, Idle  string
	Counter, Outcome string
	Locals           []invocationLocal
	Results          []invocationResult
	Counters         []struct{ Effect, Name string }
}

// GenerateInvocation emits a file of package pkg defining the type
// Invocation, the invocation bookkeeping of the controller b describes:
//
//	func NewInvocation() *Invocation
//	func (i *Invocation) Apply(event string, begin func() error) (bool, error)
//	func (i *Invocation) Observe(effect string, r effect.Result)
//	func (i *Invocation) Finish(outcome string)
//	func (i *Invocation) Running() bool
//	func (i *Invocation) Project() map[string]any
//
// Apply handles the reconcile event, which begins an invocation through
// begin (the caller starts its worker there), counts it and resets the
// locals, and the idle event, which does nothing; it reports false for any
// other event. Observe records a completed effect: its results become the
// locals the controller assigns them to, and a counted effect counts.
// Finish records the controller's result. Project returns the counter,
// the outcome, the counters and the locals under the model's names. The
// type observes the implementation only; it never consults expected
// model state.
func GenerateInvocation(pkg string, b InvocationBinding) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	if b.Reconcile == "" || b.Idle == "" || b.Counter == "" || b.Outcome == "" {
		return nil, fmt.Errorf("invocation: reconcile and idle events and the counter and outcome variables are required")
	}
	if b.Reconcile == b.Idle {
		return nil, fmt.Errorf("invocation: the reconcile and idle events must differ")
	}
	d := invocationData{Package: pkg, Reconcile: b.Reconcile, Idle: b.Idle, Counter: b.Counter, Outcome: b.Outcome}
	names := map[string]bool{b.Counter: true, b.Outcome: true}
	if b.Counter == b.Outcome {
		return nil, fmt.Errorf("invocation: the counter and outcome variables must differ")
	}
	effects := make([]string, 0, len(b.Locals))
	for e := range b.Locals {
		effects = append(effects, e)
	}
	sort.Strings(effects)
	seen := map[string]bool{}
	for _, e := range effects {
		for i, name := range b.Locals[e] {
			if name == "" {
				continue
			}
			if names[name] {
				return nil, fmt.Errorf("invocation: local %s collides with another projected variable", name)
			}
			zero := ""
			switch b.Types[name] {
			case "string":
				zero = `""`
			case "bool":
				zero = "false"
			case "int":
				zero = "int64(0)"
			default:
				return nil, fmt.Errorf("invocation: local %s of effect %s has type %q; string, bool or int expected", name, e, b.Types[name])
			}
			if !seen[name] {
				seen[name] = true
				d.Locals = append(d.Locals, invocationLocal{Name: name, Zero: zero})
			}
			d.Results = append(d.Results, invocationResult{Effect: e, Index: i, Name: name})
		}
	}
	counted := make([]string, 0, len(b.Counters))
	for e := range b.Counters {
		counted = append(counted, e)
	}
	sort.Strings(counted)
	for _, e := range counted {
		name := b.Counters[e]
		if name == "" || names[name] || seen[name] {
			return nil, fmt.Errorf("invocation: counter of effect %s needs a distinct variable name, got %q", e, name)
		}
		names[name] = true
		d.Counters = append(d.Counters, struct{ Effect, Name string }{e, name})
	}
	return render("invocation", invocationTemplate, d)
}

const invocationTemplate = Header + `

package {{.Package}}

import (
	"fmt"

	"github.com/phoban01/generative-architecture/pkg/effect"
)

// Invocation is the invocation bookkeeping of the controller: the
// invocations begun, the controller's result, the counted effects and the
// Go locals the controller assigns from effect results. It observes the
// implementation only and never consults expected model state.
type Invocation struct {
	count   int64
	outcome string
	running bool
	locals  map[string]any
{{- range .Counters}}
	{{.Name}} int64
{{- end}}
}

// NewInvocation starts the bookkeeping with every local at its zero value.
func NewInvocation() *Invocation {
	i := &Invocation{locals: map[string]any{}}
	i.reset()
	return i
}

func (i *Invocation) reset() {
{{- range .Locals}}
	i.locals[{{q .Name}}] = {{.Zero}}
{{- end}}
}

// Running reports whether an invocation began and did not finish.
func (i *Invocation) Running() bool { return i.running }

// Apply handles the invocation events: {{q .Reconcile}} begins an
// invocation through begin, counts it and resets the locals; {{q .Idle}}
// does nothing. It returns false when event names neither.
func (i *Invocation) Apply(event string, begin func() error) (bool, error) {
	switch event {
	case {{q .Idle}}:
		return true, nil
	case {{q .Reconcile}}:
		if i.running {
			return true, fmt.Errorf("invocation still running")
		}
		if err := begin(); err != nil {
			return true, err
		}
		i.count++
		i.reset()
		i.outcome = ""
		i.running = true
		return true, nil
	}
	return false, nil
}

// Observe records a completed effect: its results become the locals the
// controller assigns them to, and a counted effect counts.
func (i *Invocation) Observe(effect string, r effect.Result) {
	switch effect {
{{- range .Counters}}
	case {{q .Effect}}:
		i.{{.Name}}++
{{- end}}
	}
{{- range .Results}}
	if effect == {{q .Effect}} {
		if v, ok := r.Snapshot[{{q (printf "result%d" .Index)}}]; ok {
			i.locals[{{q .Name}}] = v
		}
	}
{{- end}}
}

// Finish records the controller's result and ends the invocation.
func (i *Invocation) Finish(outcome string) {
	i.outcome = outcome
	i.running = false
}

// Project returns the bookkeeping under the model's names.
func (i *Invocation) Project() map[string]any {
	out := map[string]any{{"{"}}{{q .Counter}}: i.count, {{q .Outcome}}: i.outcome{{"}"}}
{{- range .Counters}}
	out[{{q .Name}}] = i.{{.Name}}
{{- end}}
	for name, v := range i.locals {
		out[name] = v
	}
	return out
}
`

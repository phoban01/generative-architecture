package adaptergen

import (
	"fmt"
	"sort"

	cr "generative-architecture/pkg/controllerruntime"
)

// RuntimeBinding names the actions and state variables of a
// controller-runtime group model (controllerruntime.BuildGroupModel) as the
// composed model names them. Only listed actions are routed and only listed
// variables are projected.
type RuntimeBinding struct {
	// Events maps a group-model action to its replay event.
	Events map[string]string
	// Variables maps a group-model state variable to its model variable.
	Variables map[string]string
}

// workerCalls is the worker method that drives each runtime event kind.
// A call that returns no error is wrapped so every case returns one.
var workerCalls = map[string]struct {
	call    string
	failing bool
}{
	"advance": {"Advance()", false},
	"enqueue": {"Enqueue()", false},
	"start":   {"StartOne()", true},
	"finish":  {"Wait()", true},
	"fire":    {"Fire()", true},
}

// workerReads is the worker expression that observes each per-key state
// variable of a FIFO controller, by suffix. The clock variables now and due
// come from Clock.
var workerReads = map[string]string{
	"ready":      "worker.Ready()",
	"dirty":      "worker.Dirty()",
	"processing": "worker.Processing()",
	"retries":    "worker.Retries()",
	"due":        "due",
}

type runtimeCase struct {
	Event   string
	Call    string
	Failing bool
}

type runtimeRead struct {
	Name string
	Read string
}

// runtimeTable validates the binding against the group model and resolves
// each routed event to a worker call and each projected variable to a
// worker read. The single-key Worker facade drives one controller with one
// key on a FIFO queue, without startup or deletion.
func runtimeTable(m cr.GroupModel, b RuntimeBinding) ([]runtimeCase, []runtimeRead, error) {
	if len(m.Profile.Controllers) != 1 {
		return nil, nil, fmt.Errorf("runtime %s: the worker drives one controller, got %d", m.Profile.Name, len(m.Profile.Controllers))
	}
	c := m.Profile.Controllers[0]
	if len(c.Keys) != 1 {
		return nil, nil, fmt.Errorf("runtime %s: the worker drives one key, got %d", m.Profile.Name, len(c.Keys))
	}
	queue, err := m.Profile.QueueOf(c)
	if err != nil {
		return nil, nil, err
	}
	if queue != cr.QueueFIFO || c.Startup || c.Deletion {
		return nil, nil, fmt.Errorf("runtime %s: the worker drives a FIFO queue without startup or deletion", m.Profile.Name)
	}
	if len(b.Events) == 0 {
		return nil, nil, fmt.Errorf("runtime %s: at least one routed event is required", m.Profile.Name)
	}
	actions := make([]string, 0, len(b.Events))
	for a := range b.Events {
		actions = append(actions, a)
	}
	sort.Strings(actions)
	events := map[string]bool{}
	var cases []runtimeCase
	for _, a := range actions {
		name := b.Events[a]
		e, ok := m.Events[a]
		if !ok {
			return nil, nil, fmt.Errorf("runtime %s: %s is not an action of the group model", m.Profile.Name, a)
		}
		if name == "" || events[name] {
			return nil, nil, fmt.Errorf("runtime %s: action %s needs a distinct event name, got %q", m.Profile.Name, a, name)
		}
		events[name] = true
		call, ok := workerCalls[e.Kind]
		if !ok {
			return nil, nil, fmt.Errorf("runtime %s: action %s is a %s event, which the worker does not drive", m.Profile.Name, a, e.Kind)
		}
		cases = append(cases, runtimeCase{Event: name, Call: call.call, Failing: call.failing})
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Event < cases[j].Event })
	reads := map[string]string{"now": "now"}
	prefix := c.Name + "_" + c.Keys[0].ID + "_"
	for suffix, read := range workerReads {
		reads[prefix+suffix] = read
	}
	names := map[string]bool{}
	var out []runtimeRead
	for v, name := range b.Variables {
		if _, ok := m.Spec.State[v]; !ok {
			return nil, nil, fmt.Errorf("runtime %s: %s is not a state variable of the group model", m.Profile.Name, v)
		}
		read, ok := reads[v]
		if !ok {
			return nil, nil, fmt.Errorf("runtime %s: the worker does not observe %s", m.Profile.Name, v)
		}
		if name == "" || names[name] {
			return nil, nil, fmt.Errorf("runtime %s: variable %s needs a distinct model name, got %q", m.Profile.Name, v, name)
		}
		names[name] = true
		out = append(out, runtimeRead{Name: name, Read: read})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return cases, out, nil
}

// GenerateRuntime emits a file of package pkg defining the interface
// RuntimeWorker, which controllerruntime.Worker implements, and
//
//	func ApplyRuntime(worker RuntimeWorker, event string) (bool, error)
//	func ProjectRuntime(worker RuntimeWorker) map[string]any
//
// ApplyRuntime drives the worker for each event the binding routes: an
// enqueue adds the key, a start takes it, every finish outcome waits for the
// invocation, an advance moves the clock and a fire delivers a due timer.
// It reports whether event named a runtime event. ProjectRuntime reads the
// worker's queue and clock under the model's names. Both take only the
// worker: they never read expected model state, and the file imports no
// package of this module.
func GenerateRuntime(pkg string, m cr.GroupModel, b RuntimeBinding) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	cases, reads, err := runtimeTable(m, b)
	if err != nil {
		return nil, err
	}
	now, due := "_", "_"
	for _, r := range reads {
		switch r.Read {
		case "now":
			now = "now"
		case "due":
			due = "due"
		}
	}
	return render("runtime", runtimeTemplate, struct {
		Header   string
		Package  string
		Name     string
		Cases    []runtimeCase
		Reads    []runtimeRead
		Now, Due string
	}{Header, pkg, m.Profile.Name, cases, reads, now, due})
}

const runtimeTemplate = `{{.Header}}

package {{.Package}}

// RuntimeWorker is the single-key controller-runtime worker the replay
// drives. controllerruntime.Worker implements it.
type RuntimeWorker interface {
	Enqueue()
	StartOne() error
	Wait() error
	Advance()
	Fire() error
	Ready() bool
	Dirty() bool
	Processing() bool
	Retries() int64
	Clock() (now, due int64)
}

// ApplyRuntime drives worker for the runtime event of {{.Name}} named event.
// It returns false when event names no runtime event.
func ApplyRuntime(worker RuntimeWorker, event string) (bool, error) {
	switch event {
{{- range .Cases}}
	case {{q .Event}}:
{{- if .Failing}}
		return true, worker.{{.Call}}
{{- else}}
		worker.{{.Call}}
		return true, nil
{{- end}}
{{- end}}
	}
	return false, nil
}

// ProjectRuntime reads the worker's queue and clock under the model's
// names. It observes the worker only and never consults expected model
// state.
func ProjectRuntime(worker RuntimeWorker) map[string]any {
{{- if or (ne .Now "_") (ne .Due "_")}}
	{{.Now}}, {{.Due}} := worker.Clock()
{{- end}}
	return map[string]any{
{{- range .Reads}}
		{{q .Name}}: {{.Read}},
{{- end}}
	}
}
`

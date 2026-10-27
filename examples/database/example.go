package database

import (
	"fmt"
	"os"
	"path/filepath"

	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/dynamicgen"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/watchgen"
)

type Example struct {
	Dynamic     dynamicgen.Result
	Resources   dynamicgen.Catalog
	Watches     watchgen.Result
	Program     goflow.Program
	Spec        model.Spec
	Environment []EnvironmentAction
	// Observed lists the variables replay compares: the watch summary's
	// state and every abstraction variable.
	Observed    []string
	Model       *model.Compiled
	Constrained *model.Compiled
	// ReconcileProcess is Program as an IR process; its node IDs are the model's
	// process events. The generated gating is generated from it.
	ReconcileProcess ir.Process
	// Results names, per effect, the Go locals Reconcile assigns the
	// Store call's results to, in result order.
	Results map[string][]string
	// Locals are the abstraction's locals and their types.
	Locals map[string]string
	// emits is the watch event each action raises.
	emits map[string]watchEvent
}

func Load(root string) (Example, error) {
	source, err := os.ReadFile(filepath.Join(root, "controller.go"))
	if err != nil {
		return Example{}, err
	}
	return LoadSource(root, source)
}

// LoadSource is also used by source-mutation tests; constraints stay unchanged.
func LoadSource(root string, source []byte) (Example, error) {
	wiring, err := os.ReadFile(filepath.Join(root, "wiring.go"))
	if err != nil {
		return Example{}, err
	}
	return LoadSources(root, source, wiring)
}

// Schedule follows the extracted branches. Only environment changes and the
// number of invocations are selected here; controller decisions come from Go.
func (e Example) Schedule(name string) (conformance.Trace, error) {
	actions, err := e.scheduleActions(name)
	if err != nil {
		return conformance.Trace{}, err
	}
	return conformance.Scenario(e.Model, actions...)
}

// scheduleActions returns the actions of schedule name. On error it returns
// the actions taken before the step that failed.
func (e Example) scheduleActions(name string) ([]string, error) {
	actions := []string{}
	state, err := e.Model.Initial()
	if err != nil {
		return actions, err
	}
	advance := func(name string) error {
		next, err := e.Model.Next(state, name)
		if err != nil {
			return err
		}
		state = next
		actions = append(actions, name)
		return nil
	}
	invoke := func() error {
		if err := advance("reconcile"); err != nil {
			return err
		}
		for steps := 0; state["finished"] != true; steps++ {
			if steps > 64 {
				return fmt.Errorf("process did not return")
			}
			id := state["pc"].(int64)
			if id < 0 || int(id) >= len(e.Program.Nodes) {
				return fmt.Errorf("invalid process counter")
			}
			n := e.Program.Nodes[id]
			if err := advance(fmt.Sprintf("node%d_%s", n.ID, n.Kind)); err != nil {
				return err
			}
		}
		return nil
	}
	contract := "true"
	switch name {
	case "absent", "pending", "provisioned", "repeated":
	case "unsupported":
		contract = "false"
	default:
		return actions, fmt.Errorf("unknown scenario %s", name)
	}
	discover, err := e.EnvironmentEvent(contractVariable, contract)
	if err != nil {
		return actions, err
	}
	provision, err := e.EnvironmentEvent(instanceEndpointVariable, `"db-1.internal"`)
	if err != nil {
		return actions, err
	}
	if err = advance(discover.Name); err != nil {
		return actions, err
	}
	if name == "absent" {
		if err = advance("removeDatabaseBeforeRead"); err != nil {
			return actions, err
		}
	}
	if err = advance(deliverDatabase); err != nil {
		return actions, err
	}
	if err = invoke(); err != nil {
		return actions, err
	}
	if name == "pending" || name == "provisioned" || name == "repeated" {
		if err = advance(deliverInstance); err != nil {
			return actions, err
		}
		if err = invoke(); err != nil {
			return actions, err
		}
	}
	if name == "provisioned" || name == "repeated" {
		if err = advance(provision.Name); err != nil {
			return actions, err
		}
		if err = advance(deliverInstance); err != nil {
			return actions, err
		}
		if err = invoke(); err != nil {
			return actions, err
		}
	}
	if name == "repeated" {
		if err = advance(deliverDatabase); err != nil {
			return actions, err
		}
		if err = invoke(); err != nil {
			return actions, err
		}
	}
	return actions, nil
}

func (e Example) CheckConstraints(trace conformance.Trace) error {
	for i, s := range trace.States {
		if err := e.Constrained.Check(s.Values, s.Event); err != nil {
			return fmt.Errorf("step %d (%s): %w", i, s.Event, err)
		}
	}
	return nil
}

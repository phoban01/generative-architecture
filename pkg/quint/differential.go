package quint

import (
	"fmt"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/model"
	"reflect"
)

// Stats counts the evaluations of a differential check.
type Stats struct {
	// Traces is the number of traces replayed.
	Traces int `yaml:"traces"`
	// Transitions is the number of actions evaluated with the original CEL,
	// one per trace state after init.
	Transitions int `yaml:"transitions"`
	// Checks is the number of states on which every property was evaluated.
	Checks int `yaml:"checks"`
	// Coverage counts transitions per action.
	Coverage map[string]int `yaml:"coverage"`
	// Reached counts, per action, the traces that take it at least once.
	Reached map[string]int `yaml:"reached"`
}

// Differential replays Quint ITF traces through the original CEL of m: from
// the initial state, every state must equal Compiled.Next of its predecessor
// and must satisfy Compiled.Check. The traces came from the Quint lowering
// of the same model, so any disagreement is a translation or tool defect and
// is returned as a *conformance.Failure of category tool naming the trace,
// step and event. It is sampled evidence for any TransitionModel, not a
// proof of translation equivalence, and fails closed on an empty input.
func Differential(m *model.Compiled, traces []conformance.Trace) (Stats, error) {
	stats := Stats{Coverage: map[string]int{}, Reached: map[string]int{}}
	if m == nil {
		return stats, tool(0, "", fmt.Errorf("compiled model required"))
	}
	if len(traces) == 0 {
		return stats, tool(0, "", fmt.Errorf("no traces to check"))
	}
	for n, trace := range traces {
		if trace.Status != "ok" || len(trace.States) == 0 || trace.States[0].Event != "init" {
			return stats, tool(0, "", fmt.Errorf("trace %d: expected an ok trace beginning at init", n))
		}
		expected, err := m.Initial()
		if err != nil {
			return stats, tool(0, "", fmt.Errorf("trace %d: %w", n, err))
		}
		for i, state := range trace.States {
			if i > 0 {
				expected, err = m.Next(expected, state.Event)
				if err != nil {
					return stats, tool(i, state.Event, fmt.Errorf("trace %d step %d (%s): CEL rejects the Quint transition: %w", n, i, state.Event, err))
				}
				stats.Transitions++
				stats.Coverage[state.Event]++
			}
			if !reflect.DeepEqual(expected, state.Values) {
				return stats, tool(i, state.Event, fmt.Errorf("trace %d step %d (%s): CEL/trace mismatch: CEL %v, Quint %v", n, i, state.Event, expected, state.Values))
			}
			if err = m.Check(expected, state.Event); err != nil {
				return stats, tool(i, state.Event, fmt.Errorf("trace %d step %d (%s): CEL rejects a state Quint accepted: %w", n, i, state.Event, err))
			}
			stats.Checks++
		}
		reached := map[string]bool{}
		for _, state := range trace.States[1:] {
			reached[state.Event] = true
		}
		for event := range reached {
			stats.Reached[event]++
		}
		stats.Traces++
	}
	return stats, nil
}

func tool(step int, event string, err error) error {
	return &conformance.Failure{Category: conformance.CategoryTool, Step: step, Event: event, Err: err}
}

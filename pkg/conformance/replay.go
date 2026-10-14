// Package conformance compares implementation projections to independently
// checked model traces. Adapters never receive expected state.
package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"generative-architecture/pkg/model"
	"os"
	"reflect"
	"strconv"
)

type State struct {
	Event  string
	Values map[string]any
}
type Trace struct {
	Status string
	States []State
}
type Adapter interface {
	Apply(context.Context, string) error
	Project(context.Context) (map[string]any, error)
}
type Report struct {
	Steps    int            `yaml:"steps"`
	Coverage map[string]int `yaml:"coverage"`
}

// Replay checks the model and the implementation step by step. Every error it
// returns is a *Failure whose Category says whether a property was violated
// (safety), the trace, CEL or projection disagreed (conformance), the adapter
// reported an unsupported construct (unsupported) or something else broke
// (tool); see CategoryOf.
func Replay(ctx context.Context, m *model.Compiled, trace Trace, a Adapter) (Report, error) {
	return replay(ctx, m, trace, a, nil)
}

// ReplayObserved checks an explicit observable projection. Model transitions and
// properties are still checked in full; unobservable program counters are not
// fabricated by the implementation adapter.
func ReplayObserved(ctx context.Context, m *model.Compiled, trace Trace, a Adapter, fields []string) (Report, error) {
	if len(fields) == 0 {
		return Report{}, fail(CategoryTool, 0, "", fmt.Errorf("observable fields required"))
	}
	return replay(ctx, m, trace, a, fields)
}
func replay(ctx context.Context, m *model.Compiled, trace Trace, a Adapter, fields []string) (Report, error) {
	report := Report{Coverage: map[string]int{}}
	if trace.Status != "ok" || len(trace.States) == 0 || trace.States[0].Event != "init" {
		return report, fail(CategoryTool, 0, "", fmt.Errorf("expected an ok trace beginning at init"))
	}
	expected, err := m.Initial()
	if err != nil {
		return report, fail(CategoryTool, 0, "", err)
	}
	for i, state := range trace.States {
		if err := ctx.Err(); err != nil {
			return report, fail(CategoryTool, i, state.Event, err)
		}
		if i > 0 {
			expected, err = m.Next(expected, state.Event)
			if err != nil {
				return report, fail(CategoryConformance, i, state.Event, fmt.Errorf("model step %d: %w", i, err))
			}
		}
		if !reflect.DeepEqual(expected, state.Values) {
			return report, fail(CategoryConformance, i, state.Event, fmt.Errorf("CEL/trace mismatch at step %d", i))
		}
		if err = m.Check(expected, state.Event); err != nil {
			return report, fail(CategorySafety, i, state.Event, fmt.Errorf("step %d: %w", i, err))
		}
		if i > 0 {
			if err = a.Apply(ctx, state.Event); err != nil {
				return report, fail("", i, state.Event, fmt.Errorf("adapter step %d (%s): %w", i, state.Event, err))
			}
			report.Coverage[state.Event]++
		}
		actual, err := a.Project(ctx)
		if err != nil {
			return report, fail("", i, state.Event, fmt.Errorf("adapter projection at step %d (%s): %w", i, state.Event, err))
		}
		projected := expected
		if fields != nil {
			projected = map[string]any{}
			for _, field := range fields {
				v, ok := expected[field]
				if !ok {
					return report, fail(CategoryTool, i, state.Event, fmt.Errorf("unknown observation field %s", field))
				}
				if _, exists := projected[field]; exists {
					return report, fail(CategoryTool, i, state.Event, fmt.Errorf("duplicate observation field %s", field))
				}
				projected[field] = v
			}
		}
		if !reflect.DeepEqual(projected, actual) {
			return report, fail(CategoryConformance, i, state.Event, fmt.Errorf("conformance mismatch at step %d (%s): expected %v, got %v", i, state.Event, projected, actual))
		}
		report.Steps = i
	}
	return report, nil
}

// Scenario builds a trace with original CEL. Quint ITF traces are a second,
// independent source and should also be replayed to exercise the lowering.
func Scenario(m *model.Compiled, actions ...string) (Trace, error) {
	initial, err := m.Initial()
	if err != nil {
		return Trace{}, err
	}
	t := Trace{Status: "ok", States: []State{{Event: "init", Values: initial}}}
	state := initial
	for _, a := range actions {
		state, err = m.Next(state, a)
		if err != nil {
			return t, err
		}
		t.States = append(t.States, State{Event: a, Values: state})
	}
	return t, nil
}
func decode(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		return strconv.ParseInt(string(x), 10, 64)
	case map[string]any:
		if n, ok := x["#bigint"]; ok {
			s, ok := n.(string)
			if !ok {
				return nil, fmt.Errorf("invalid ITF integer")
			}
			return strconv.ParseInt(s, 10, 64)
		}
		out := map[string]any{}
		for k, v := range x {
			if k == "#meta" {
				continue
			}
			if len(k) > 0 && k[0] == '#' {
				return nil, fmt.Errorf("unsupported ITF tag %s", k)
			}
			d, err := decode(v)
			if err != nil {
				return nil, err
			}
			out[k] = d
		}
		return out, nil
	default:
		return v, nil
	}
}

// ReadITF supports the scalar and record state emitted by TransitionModel.
func ReadITF(path string) (Trace, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Trace{}, err
	}
	var raw struct {
		Meta struct {
			Status string `json:"status"`
		} `json:"#meta"`
		States []map[string]any `json:"states"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&raw); err != nil {
		return Trace{}, err
	}
	t := Trace{Status: raw.Meta.Status}
	for _, s := range raw.States {
		v, err := decode(s)
		if err != nil {
			return t, err
		}
		values := v.(map[string]any)
		event, ok := values["event"].(string)
		if !ok {
			return t, fmt.Errorf("missing event")
		}
		delete(values, "event")
		t.States = append(t.States, State{Event: event, Values: values})
	}
	return t, nil
}

package architecture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
)

type Trace struct {
	Status string
	States []map[string]any
}

func DecodeITF(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		return strconv.ParseInt(string(x), 10, 64)
	case []any:
		out := []any{}
		for _, item := range x {
			d, e := DecodeITF(item)
			if e != nil {
				return nil, e
			}
			out = append(out, d)
		}
		return out, nil
	case map[string]any:
		if n, ok := x["#bigint"]; ok {
			s, ok := n.(string)
			if !ok {
				return nil, fmt.Errorf("invalid ITF bigint")
			}
			return strconv.ParseInt(s, 10, 64)
		}
		if entries, ok := x["#map"]; ok {
			pairs, ok := entries.([]any)
			if !ok {
				return nil, fmt.Errorf("invalid ITF map")
			}
			out := map[string]any{}
			for _, p := range pairs {
				pair, ok := p.([]any)
				if !ok || len(pair) != 2 {
					return nil, fmt.Errorf("invalid ITF map entry")
				}
				key, ok := pair[0].(string)
				if !ok {
					return nil, fmt.Errorf("ITF map key must be string")
				}
				if _, ok := out[key]; ok {
					return nil, fmt.Errorf("duplicate ITF map key")
				}
				d, e := DecodeITF(pair[1])
				if e != nil {
					return nil, e
				}
				out[key] = d
			}
			return out, nil
		}
		out := map[string]any{}
		for k, item := range x {
			if k == "#meta" {
				continue
			}
			if len(k) > 0 && k[0] == '#' {
				return nil, fmt.Errorf("unsupported ITF tag %s", k)
			}
			d, e := DecodeITF(item)
			if e != nil {
				return nil, e
			}
			out[k] = d
		}
		return out, nil
	}
	return v, nil
}
func ReadTrace(path string) (Trace, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return Trace{}, e
	}
	raw := struct {
		Meta struct {
			Status string `json:"status"`
		} `json:"#meta"`
		States []map[string]any `json:"states"`
	}{}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e = d.Decode(&raw); e != nil {
		return Trace{}, e
	}
	if !contains([]string{"ok", "violation"}, raw.Meta.Status) || len(raw.States) == 0 {
		return Trace{}, fmt.Errorf("expected successful or invariant-violation ITF trace")
	}
	t := Trace{Status: raw.Meta.Status}
	for _, s := range raw.States {
		decoded, e := DecodeITF(s)
		if e != nil {
			return t, e
		}
		state := decoded.(map[string]any)
		event, ok := state["event"].(map[string]any)
		if !ok {
			return t, fmt.Errorf("missing trace event")
		}
		for _, k := range []string{"name", "infra", "host"} {
			if _, ok := event[k].(string); !ok {
				return t, fmt.Errorf("invalid event %s", k)
			}
		}
		if _, ok := event["accepted"].(bool); !ok {
			return t, fmt.Errorf("invalid event acceptance")
		}
		t.States = append(t.States, state)
	}
	if t.States[0]["event"].(map[string]any)["name"] != "init" {
		return t, fmt.Errorf("trace must begin at init")
	}
	return t, nil
}
func clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, v := range x {
			m[k] = clone(v)
		}
		return m
	case []any:
		a := []any{}
		for _, v := range x {
			a = append(a, clone(v))
		}
		return a
	}
	return v
}
func (m *Model) projection(state map[string]any) map[string]any {
	out := map[string]any{}
	for _, s := range m.States {
		out[s.Name] = state[s.Name]
	}
	return out
}

type ReplayResult struct {
	OK               bool           `yaml:"ok"`
	Steps            int            `yaml:"steps"`
	Action           string         `yaml:"action,omitempty"`
	ExpectedAccepted bool           `yaml:"expectedAccepted"`
	ActualAccepted   bool           `yaml:"actualAccepted"`
	Expected         map[string]any `yaml:"expected,omitempty"`
	Actual           map[string]any `yaml:"actual,omitempty"`
	Coverage         map[string]int `yaml:"coverage"`
	Prefix           []any          `yaml:"prefix,omitempty"`
}

func (m *Model) Replay(t Trace, bug bool) (ReplayResult, error) {
	return m.ReplayAdapter(t, NewSimulator(m.Project.System, bug))
}

// Adapter executes implementation operations without receiving expected states.
// Project must read implementation state, not the contract or trace oracle.
type Adapter interface {
	Apply(string, map[string]string) (bool, error)
	Project() (map[string]any, error)
}

func (m *Model) ReplayAdapter(t Trace, sim Adapter) (ReplayResult, error) {
	result := ReplayResult{OK: true, Coverage: map[string]int{}}
	for index, state := range t.States {
		event := state["event"].(map[string]any)
		accepted := true
		if index > 0 {
			a, ok := m.Action(event["name"].(string))
			if !ok {
				return result, fmt.Errorf("unknown trace action")
			}
			args := map[string]string{}
			for _, p := range a.Params {
				args[p] = event[p].(string)
			}
			var err error
			accepted, err = sim.Apply(a.ID, args)
			if err != nil {
				return result, err
			}
			key := a.ID
			if a.ID == "mac.publish" || a.ID == "service.execute" {
				if accepted {
					key += ":accepted"
				} else {
					key += ":rejected"
				}
			}
			result.Coverage[key]++
			result.Prefix = append(result.Prefix, event)
		}
		expected := m.projection(state)
		actual, err := sim.Project()
		if err != nil {
			return result, err
		}
		if accepted != event["accepted"].(bool) || !reflect.DeepEqual(expected, actual) {
			result.OK = false
			result.Steps = index
			result.Action = event["name"].(string)
			result.ExpectedAccepted = event["accepted"].(bool)
			result.ActualAccepted = accepted
			result.Expected = expected
			result.Actual = actual
			return result, nil
		}
	}
	result.Steps = len(t.States) - 1
	result.Prefix = nil
	return result, nil
}
func (m *Model) activation(c Contract, state map[string]any) map[string]any {
	out := m.constantValues()
	for name := range c.State {
		out[name] = state[c.ID+"_"+name]
	}
	for name, p := range c.Ports {
		if p.Direction == "out" {
			out[name] = state[c.ID+"_"+p.State]
		} else {
			out[name] = state[m.InputState[c.ID+"."+name]]
		}
	}
	return out
}
func (m *Model) constantValues() map[string]any {
	p := m.Project
	s := p.System
	infras := []string{}
	for _, i := range s.InfraMachines {
		infras = append(infras, i.ID)
	}
	return map[string]any{"HOSTS": s.Hosts, "INFRAS": infras, "MAX_EPOCH": s.MaxEpoch, "EMPTY_ASSIGNMENT": map[string]any{"machineId": "", "epoch": int64(0)}}
}

// ValidateCEL checks original expressions with cel-go, independently of Quint lowering.
func (m *Model) ValidateCEL(t Trace, e *Evaluator) (map[string]int, error) {
	initial := map[string]any{}
	for _, r := range m.Project.System.Components {
		c := m.Project.Contracts[r.ID]
		for name, s := range c.State {
			entries := map[string]any{}
			for _, key := range m.constantValues()[s.Init.Domain].([]string) {
				v, err := e.Eval(s.Init.Value, m.constantValues())
				if err != nil {
					return nil, err
				}
				entries[key] = v
			}
			initial[c.ID+"_"+name] = entries
		}
	}
	if !reflect.DeepEqual(initial, m.projection(t.States[0])) {
		return nil, fmt.Errorf("CEL/Quint initialization mismatch")
	}
	for index := 1; index < len(t.States); index++ {
		previous, next := t.States[index-1], t.States[index]
		event := next["event"].(map[string]any)
		a, ok := m.Action(event["name"].(string))
		if !ok {
			return nil, fmt.Errorf("unknown trace action")
		}
		c := m.Project.Contracts[a.Owner]
		var source Action
		for _, candidate := range c.Actions {
			if a.ID == c.ID+"."+candidate.Name {
				source = candidate
				break
			}
		}
		vars := m.activation(c, previous)
		for _, param := range source.Params {
			vars[param] = event[param]
		}
		for _, local := range source.Locals {
			v, err := e.Eval(local.Value, vars)
			if err != nil {
				return nil, err
			}
			vars[local.Name] = v
		}
		guard, err := e.Eval(source.Guard, vars)
		if err != nil {
			return nil, err
		}
		if guard != true {
			return nil, fmt.Errorf("CEL guard false at step %d", index)
		}
		projected := clone(m.projection(previous)).(map[string]any)
		for _, name := range keys(source.Updates) {
			u := source.Updates[name]
			if u.When != nil {
				when, err := e.Eval(*u.When, vars)
				if err != nil {
					return nil, err
				}
				if when == false {
					continue
				}
				if when != true {
					return nil, fmt.Errorf("non-boolean update condition")
				}
			}
			key, err := e.Eval(u.Key, vars)
			if err != nil {
				return nil, err
			}
			k, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("non-string update key")
			}
			target, ok := projected[c.ID+"_"+name].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid projected map")
			}
			if _, ok := target[k]; !ok {
				return nil, fmt.Errorf("update key outside finite map")
			}
			value, err := e.Eval(u.Value, vars)
			if err != nil {
				return nil, err
			}
			target[k] = value
		}
		accepted, err := e.Eval(source.Accepted, vars)
		if err != nil {
			return nil, err
		}
		if accepted != event["accepted"] || !reflect.DeepEqual(projected, m.projection(next)) {
			return nil, fmt.Errorf("CEL/Quint mismatch at step %d (%s)", index, a.ID)
		}
	}
	violations := map[string]int{}
	for _, state := range t.States {
		vars := m.constantValues()
		vars["event"] = state["event"]
		for _, r := range m.Project.System.Components {
			c := m.Project.Contracts[r.ID]
			vars[c.ID] = m.activation(c, state)
		}
		for _, prop := range m.Project.System.Properties {
			v, err := e.Eval(prop.Expression, vars)
			if err != nil {
				return nil, err
			}
			if v != true {
				return nil, fmt.Errorf("CEL invariant failed: %s", prop.Name)
			}
		}
		for _, prop := range m.Project.System.SafetyQuestions {
			v, err := e.Eval(prop.Expression, vars)
			if err != nil {
				return nil, err
			}
			if v == false {
				violations[prop.Name]++
			} else if v != true {
				return nil, fmt.Errorf("non-boolean property")
			}
		}
	}
	return violations, nil
}

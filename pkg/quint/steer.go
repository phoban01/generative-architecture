package quint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/receipt"
)

// Sampling strategies, as a receipt records them.
const (
	// StrategyUniform samples the model's own step relation: at each step
	// the Quint simulator draws uniformly among the enabled actions.
	StrategyUniform = "uniform"
	// StrategyWeighted samples a step relation that lists each action as
	// many times as its weight. At each step an enabled action is drawn
	// with probability proportional to its weight among the enabled
	// actions. Every weight is positive, so steering changes how often an
	// action is drawn, never whether it can be.
	StrategyWeighted = "weighted"
)

// MaxWeight bounds a steering weight. It keeps the steered step relation
// small.
const MaxWeight = 64

// Steering selects how Quint draws the next action. The zero value is
// StrategyUniform. A steered sample is still a bounded sample of the same
// transition relation: it is not exhaustive, and it makes the actions it
// does not prefer rarer.
type Steering struct {
	// Weights maps action names to a weight from 1 to MaxWeight. An action
	// without an entry has weight 1.
	Weights map[string]int
}

// Strategy names the steering strategy.
func (s Steering) Strategy() string {
	if len(s.Weights) == 0 {
		return StrategyUniform
	}
	return StrategyWeighted
}

// steeredStep names the step action Sample adds for a weighted strategy.
// The suffix keeps it apart from model identifiers; module checks that
// the model does not use it.
const steeredStep = "steeredStep__"

var stepPattern = regexp.MustCompile(`(?m)^ action step = any \{ (.*) \}$`)

// Actions lists the actions of m's step relation in model order.
func Actions(m *model.Compiled) ([]string, error) {
	match := stepPattern.FindStringSubmatch(m.Quint())
	if match == nil {
		return nil, fmt.Errorf("model has no step relation")
	}
	return strings.Split(match[1], ", "), nil
}

// ProcessActions lists the actions of m that advance a process: its
// internal steps (named node<id>_<kind>) and the outcomes of its effects
// (named <effect>_<outcome>), less the outcomes named in except.
func ProcessActions(m *model.Compiled, except ...string) ([]string, error) {
	actions, err := Actions(m)
	if err != nil {
		return nil, err
	}
	effects := map[string]bool{}
	for _, d := range m.Effects().Descriptors() {
		effects[d.ID] = true
	}
	var out []string
	for _, a := range actions {
		if _, ok := conformance.NodeID(a); ok {
			out = append(out, a)
			continue
		}
		if id, outcome, ok := strings.Cut(a, "_"); ok && effects[id] && !slices.Contains(except, outcome) {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("model has no process actions to prefer")
	}
	return out, nil
}

// PreferProcess is a weighted strategy that steers traces towards process
// completion: it gives weight to m's ProcessActions, less the effect
// outcomes named in except, which keep weight 1 like every other action.
// Naming the failure outcomes in except makes a completed process more
// likely than a failed one.
func PreferProcess(m *model.Compiled, weight int, except ...string) (Steering, error) {
	prefer, err := ProcessActions(m, except...)
	if err != nil {
		return Steering{}, err
	}
	s := Steering{Weights: map[string]int{}}
	for _, a := range prefer {
		s.Weights[a] = weight
	}
	if _, err := s.module(m); err != nil {
		return Steering{}, err
	}
	return s, nil
}

// module returns the Quint module to sample. It fails closed on an
// unknown action, a weight out of range, or a strategy whose weights are
// all equal, which is uniform sampling under another name.
func (s Steering) module(m *model.Compiled) (string, error) {
	if s.Strategy() == StrategyUniform {
		return m.Quint(), nil
	}
	actions, err := Actions(m)
	if err != nil {
		return "", err
	}
	for name, w := range s.Weights {
		if !slices.Contains(actions, name) {
			return "", fmt.Errorf("weighted sampling: unknown action %q", name)
		}
		if w < 1 || w > MaxWeight {
			return "", fmt.Errorf("weighted sampling: weight of %s must be from 1 to %d, got %d", name, MaxWeight, w)
		}
	}
	var listed []string
	distinct := map[int]bool{}
	for _, a := range actions {
		w := s.weight(a)
		distinct[w] = true
		for range w {
			listed = append(listed, a)
		}
	}
	if len(distinct) < 2 {
		return "", fmt.Errorf("weighted sampling: every action has the same weight; use uniform sampling")
	}
	src := m.Quint()
	if strings.Contains(src, steeredStep) {
		return "", fmt.Errorf("model already uses the name %s", steeredStep)
	}
	end := strings.LastIndex(src, "}")
	if end < 0 {
		return "", fmt.Errorf("model module is not closed")
	}
	return fmt.Sprintf("%s action %s = any { %s }\n%s", src[:end], steeredStep, strings.Join(listed, ", "), src[end:]), nil
}

func (s Steering) weight(action string) int {
	if w, ok := s.Weights[action]; ok {
		return w
	}
	return 1
}

// stepName is the step action Quint samples under s.
func (s Steering) stepName() string {
	if s.Strategy() == StrategyUniform {
		return "step"
	}
	return steeredStep
}

// Record returns the sampling bounds and strategy of o as a receipt
// records them. Sample keeps every trace Quint produces, so the record
// needs no count of discarded traces.
func (o Options) Record() receipt.Sampling {
	r := receipt.Sampling{Samples: o.Samples, Steps: o.Steps, Seed: o.Seed, Strategy: o.Steering.Strategy()}
	if r.Strategy == StrategyWeighted {
		r.Weights = map[string]int{}
		for name, w := range o.Steering.Weights {
			r.Weights[name] = w
		}
	}
	return r
}

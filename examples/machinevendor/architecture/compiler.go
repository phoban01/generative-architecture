package architecture

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type CompiledState struct {
	Name  string `yaml:"name"`
	Owner string `yaml:"owner"`
	Type  string `yaml:"type"`
	Init  string `yaml:"init"`
}
type CompiledAction struct {
	ID       string            `yaml:"id"`
	Owner    string            `yaml:"owner"`
	Quint    string            `yaml:"quint"`
	Params   []string          `yaml:"params"`
	Guard    string            `yaml:"guard"`
	Accepted string            `yaml:"accepted"`
	Locals   []Local           `yaml:"locals,omitempty"`
	Updates  map[string]string `yaml:"updates"`
}
type Edge struct {
	From     string `yaml:"from"`
	To       string `yaml:"to"`
	Protocol string `yaml:"protocol"`
	Key      string `yaml:"key"`
	State    string `yaml:"state"`
	Evidence string `yaml:"evidence"`
}
type Model struct {
	Project    *Project          `yaml:"-"`
	States     []CompiledState   `yaml:"states"`
	Actions    []CompiledAction  `yaml:"actions"`
	Edges      []Edge            `yaml:"bindings"`
	Properties []Property        `yaml:"properties"`
	InputState map[string]string `yaml:"inputState"`
}

func (p *Project) Compile() (*Model, error) {
	if err := p.ValidateSources(); err != nil {
		return nil, err
	}
	s := p.System
	m := &Model{Project: p, InputState: map[string]string{}}
	if s.ExpressionLanguage != "cel" {
		return nil, fmt.Errorf("expected expressionLanguage cel")
	}
	if len(s.Hosts) == 0 || !unique(s.Hosts) || len(s.InfraMachines) == 0 || s.MaxEpoch < 1 {
		return nil, fmt.Errorf("invalid finite domains")
	}
	consumerIDs := []string{}
	for _, c := range s.Consumers {
		consumerIDs = append(consumerIDs, c.ID)
	}
	if !unique(consumerIDs) {
		return nil, fmt.Errorf("duplicate consumer")
	}
	infraIDs := []string{}
	for _, i := range s.InfraMachines {
		if !contains(consumerIDs, i.Consumer) {
			return nil, fmt.Errorf("unknown consumer %s", i.Consumer)
		}
		infraIDs = append(infraIDs, i.ID)
	}
	if !unique(infraIDs) {
		return nil, fmt.Errorf("duplicate InfraMachine")
	}
	componentIDs := []string{}
	for _, r := range s.Components {
		componentIDs = append(componentIDs, r.ID)
		c, ok := p.Contracts[r.ID]
		if !ok || c.ID != r.ID || !identifier.MatchString(c.ID) {
			return nil, fmt.Errorf("invalid component %s", r.ID)
		}
		if c.Version != r.Version {
			return nil, fmt.Errorf("version mismatch for %s", c.ID)
		}
		if c.Origin != "owned-example" && c.Origin != "external-contract" {
			return nil, fmt.Errorf("invalid origin %s", c.Origin)
		}
		for k, v := range c.State {
			if !identifier.MatchString(k) {
				return nil, fmt.Errorf("invalid state %s", k)
			}
			if _, err := QuintType(v.Type); err != nil {
				return nil, err
			}
		}
		for k, v := range c.Ports {
			if !identifier.MatchString(k) || !contains([]string{"in", "out"}, v.Direction) {
				return nil, fmt.Errorf("invalid port %s.%s", c.ID, k)
			}
			if v.Protocol == "" || v.Key == "" {
				return nil, fmt.Errorf("missing port protocol/key")
			}
			if _, err := QuintType(v.Type); err != nil {
				return nil, err
			}
			if v.Direction == "out" {
				if c.State[v.State].Type != v.Type {
					return nil, fmt.Errorf("invalid output state %s.%s", c.ID, k)
				}
			} else if _, ok := c.State[k]; ok {
				return nil, fmt.Errorf("input shadows owned state")
			}
		}
		names := []string{}
		for _, a := range c.Actions {
			names = append(names, a.Name)
			if !identifier.MatchString(a.Name) || len(a.Params) == 0 || !unique(a.Params) {
				return nil, fmt.Errorf("invalid action signature")
			}
			for _, v := range a.Params {
				if !contains([]string{"host", "infra"}, v) {
					return nil, fmt.Errorf("unknown parameter %s", v)
				}
			}
			for state := range a.Updates {
				if _, ok := c.State[state]; !ok {
					return nil, fmt.Errorf("action writes unowned state %s", state)
				}
			}
		}
		if !unique(names) {
			return nil, fmt.Errorf("duplicate action")
		}
	}
	if !unique(componentIDs) {
		return nil, fmt.Errorf("duplicate component")
	}
	endpoint := func(name, dir string) (string, Port, error) {
		parts := strings.Split(name, ".")
		if len(parts) != 2 {
			return "", Port{}, fmt.Errorf("unknown endpoint %s", name)
		}
		port, ok := p.Contracts[parts[0]].Ports[parts[1]]
		if !ok {
			return "", port, fmt.Errorf("unknown endpoint %s", name)
		}
		if port.Direction != dir {
			return "", port, fmt.Errorf("wrong endpoint direction %s", name)
		}
		return parts[0], port, nil
	}
	for _, b := range s.Bindings {
		owner, from, err := endpoint(b.From, "out")
		if err != nil {
			return nil, err
		}
		_, to, err := endpoint(b.To, "in")
		if err != nil {
			return nil, err
		}
		if _, ok := m.InputState[b.To]; ok {
			return nil, fmt.Errorf("ambiguous binding %s", b.To)
		}
		if from.Protocol != to.Protocol || from.Type != to.Type || from.Key != to.Key {
			return nil, fmt.Errorf("incompatible binding %s -> %s", b.From, b.To)
		}
		state := owner + "_" + from.State
		m.InputState[b.To] = state
		evidence := b.Evidence
		if evidence == "" {
			evidence = "explicit validated binding"
		}
		m.Edges = append(m.Edges, Edge{b.From, b.To, from.Protocol, from.Key, state, evidence})
	}
	global := constants()
	global["event"] = Symbol{Type: "Event", Quint: "event"}
	scopes := map[string]map[string]Symbol{}
	for _, r := range s.Components {
		c := p.Contracts[r.ID]
		scope := constants()
		for _, name := range keys(c.State) {
			d := c.State[name]
			if _, ok := scope[name]; ok {
				return nil, fmt.Errorf("state shadows constant")
			}
			scope[name] = Symbol{Type: d.Type, Quint: c.ID + "_" + name}
			global[c.ID+"."+name] = scope[name]
			if d.Init.Domain != "HOSTS" && d.Init.Domain != "INFRAS" {
				return nil, fmt.Errorf("invalid initializer domain")
			}
			t, err := mapElement(d.Type)
			if err != nil {
				return nil, err
			}
			v, err := CompileCEL(d.Init.Value, constants(), t)
			if err != nil {
				return nil, err
			}
			qt, _ := QuintType(d.Type)
			m.States = append(m.States, CompiledState{c.ID + "_" + name, c.ID, qt, d.Init.Domain + ".mapBy(_ => " + v.Quint + ")"})
		}
		for _, name := range keys(c.Ports) {
			port := c.Ports[name]
			if _, ok := constants()[name]; ok {
				return nil, fmt.Errorf("port shadows constant")
			}
			state := c.ID + "_" + port.State
			if port.Direction == "in" {
				var ok bool
				state, ok = m.InputState[c.ID+"."+name]
				if !ok {
					return nil, fmt.Errorf("unbound required input %s.%s", c.ID, name)
				}
			}
			scope[name] = Symbol{Type: port.Type, Quint: state}
			global[c.ID+"."+name] = scope[name]
		}
		scopes[c.ID] = scope
	}
	for _, r := range s.Components {
		c := p.Contracts[r.ID]
		for _, a := range c.Actions {
			scope := copyScope(scopes[c.ID])
			ca := CompiledAction{ID: c.ID + "." + a.Name, Owner: c.ID, Quint: c.ID + "_" + a.Name, Params: a.Params, Updates: map[string]string{}}
			for _, param := range a.Params {
				if _, ok := scope[param]; ok {
					return nil, fmt.Errorf("parameter shadows symbol")
				}
				scope[param] = Symbol{Type: "string", Quint: param}
			}
			for _, l := range a.Locals {
				if _, ok := scope[l.Name]; ok || !identifier.MatchString(l.Name) {
					return nil, fmt.Errorf("invalid or shadowed local")
				}
				v, err := CompileCEL(l.Value, scope, "")
				if err != nil {
					return nil, err
				}
				scope[l.Name] = Symbol{Type: v.Type, Quint: l.Name}
				ca.Locals = append(ca.Locals, Local{l.Name, Expression(v.Quint)})
			}
			for _, name := range keys(a.Updates) {
				u := a.Updates[name]
				k, err := CompileCEL(u.Key, scope, "string")
				if err != nil {
					return nil, err
				}
				t, _ := mapElement(c.State[name].Type)
				v, err := CompileCEL(u.Value, scope, t)
				if err != nil {
					return nil, err
				}
				state := c.ID + "_" + name
				update := state + ".set(" + k.Quint + ", " + v.Quint + ")"
				if u.When != nil {
					cond, err := CompileCEL(*u.When, scope, "bool")
					if err != nil {
						return nil, err
					}
					update = "if (" + cond.Quint + ") " + update + " else " + state
				}
				ca.Updates[state] = update
			}
			g, err := CompileCEL(a.Guard, scope, "bool")
			if err != nil {
				return nil, err
			}
			accepted, err := CompileCEL(a.Accepted, scope, "bool")
			if err != nil {
				return nil, err
			}
			ca.Guard = g.Quint
			ca.Accepted = accepted.Quint
			m.Actions = append(m.Actions, ca)
		}
	}
	names := []string{}
	for _, st := range m.States {
		names = append(names, st.Name)
	}
	for _, a := range m.Actions {
		names = append(names, a.Quint)
	}
	if !unique(names) {
		return nil, fmt.Errorf("generated name collision")
	}
	propertyNames := []string{}
	for _, pr := range append(append([]Property{}, s.Properties...), s.SafetyQuestions...) {
		if !identifier.MatchString(pr.Name) {
			return nil, fmt.Errorf("invalid property name")
		}
		propertyNames = append(propertyNames, pr.Name)
		v, err := CompileCEL(pr.Expression, global, "bool")
		if err != nil {
			return nil, err
		}
		m.Properties = append(m.Properties, Property{pr.Name, Expression(v.Quint)})
	}
	if !unique(propertyNames) {
		return nil, fmt.Errorf("duplicate property")
	}
	for name, steps := range p.Scenarios {
		if !identifier.MatchString(name) || len(steps) == 0 {
			return nil, fmt.Errorf("invalid scenario %s", name)
		}
		for _, step := range steps {
			a, ok := m.Action(step.Action)
			if !ok {
				return nil, fmt.Errorf("unknown scenario action %s", step.Action)
			}
			if contains(a.Params, "host") && !contains(s.Hosts, step.Host) || contains(a.Params, "infra") && !contains(infraIDs, step.Infra) {
				return nil, fmt.Errorf("unknown scenario identity")
			}
		}
	}
	return m, nil
}
func (m *Model) Action(id string) (CompiledAction, bool) {
	for _, a := range m.Actions {
		if a.ID == id {
			return a, true
		}
	}
	return CompiledAction{}, false
}
func quote(s string) string { return strconv.Quote(s) }
func quoted(xs []string) string {
	out := []string{}
	for _, s := range xs {
		out = append(out, quote(s))
	}
	return strings.Join(out, ", ")
}
func (m *Model) Quint() string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	s := m.Project.System
	w("// GENERATED from YAML and CEL by the Go architecture compiler.\nmodule System {\n  type Assignment = { machineId: str, epoch: int }\n  pure val EMPTY_ASSIGNMENT = { machineId: \"\", epoch: 0 }\n  pure val HOSTS = Set(%s)\n", quoted(s.Hosts))
	infras := []string{}
	for _, i := range s.InfraMachines {
		infras = append(infras, i.ID)
	}
	w("  pure val INFRAS = Set(%s)\n  pure val MAX_EPOCH = %d\n", quoted(infras), s.MaxEpoch)
	for _, st := range m.States {
		w("  var %s: %s\n", st.Name, st.Type)
	}
	w("  var tick: int\n  var event: { name: str, host: str, infra: str, accepted: bool }\n\n  action init = all {\n")
	for _, st := range m.States {
		w("    %s' = %s,\n", st.Name, st.Init)
	}
	w("    tick' = 0,\n    event' = { name: \"init\", host: %s, infra: %s, accepted: true }\n  }\n", quote(s.Hosts[0]), quote(infras[0]))
	frame := func(updates map[string]string) {
		for _, st := range m.States {
			value, ok := updates[st.Name]
			if !ok {
				value = st.Name
			}
			w("      %s' = %s,\n", st.Name, value)
		}
	}
	for _, a := range m.Actions {
		w("\n  action %s(%s) = {\n", a.Quint, strings.Join(a.Params, ", "))
		for _, l := range a.Locals {
			w("    val %s = %s\n", l.Name, l.Value)
		}
		w("    all {\n      %s,\n", a.Guard)
		frame(a.Updates)
		host, infra := quote(s.Hosts[0]), quote(infras[0])
		if contains(a.Params, "host") {
			host = "host"
		}
		if contains(a.Params, "infra") {
			infra = "infra"
		}
		w("      tick' = tick + 1,\n      event' = { name: %s, host: %s, infra: %s, accepted: %s }\n    }\n  }\n", quote(a.ID), host, infra, a.Accepted)
	}
	w("\n  action halt = all { false,\n")
	frame(nil)
	w("    tick' = tick, event' = event\n  }\n\n  action step = {\n    nondet host = HOSTS.oneOf()\n    nondet infra = INFRAS.oneOf()\n    any {\n")
	for i, a := range m.Actions {
		suffix := ","
		if i == len(m.Actions)-1 {
			suffix = ""
		}
		w("      %s(%s)%s\n", a.Quint, strings.Join(a.Params, ", "), suffix)
	}
	w("    }\n  }\n")
	for _, p := range m.Properties {
		w("  val %s = %s\n", p.Name, p.Expression)
	}
	for _, name := range keys(m.Project.Scenarios) {
		w("\n  action %sStep =\n", name)
		for i, step := range m.Project.Scenarios[name] {
			a, _ := m.Action(step.Action)
			args := []string{}
			for _, param := range a.Params {
				if param == "host" {
					args = append(args, quote(step.Host))
				} else {
					args = append(args, quote(step.Infra))
				}
			}
			w("    if (tick == %d) %s(%s) else\n", i, a.Quint, strings.Join(args, ", "))
		}
		w("    halt\n")
	}
	w("}\n")
	return b.String()
}
func (m *Model) LikeC4() string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	p := m.Project
	w("// GENERATED from the same resolved bindings as System.qnt.\nspecification {\n  element owned { style { color blue } }\n  element external { style { color amber } }\n  element cluster { style { color green } }\n}\nmodel {\n")
	for _, r := range p.System.Components {
		c := p.Contracts[r.ID]
		kind, evidence := "owned", "authored contract; simulated Go adapter"
		if c.Origin == "external-contract" {
			kind, evidence = "external", "authored assumption; upstream conformance untested"
		}
		if p.Sources != nil {
			for _, controller := range p.Sources.Controllers {
				if controller.Component == c.ID {
					evidence += "; Go structure checked: " + controller.GoType + "; primary " + controller.Primary + "; owns (declared) " + controller.Owns
				}
			}
		}
		w("  %s = %s %s { description %s }\n", c.ID, kind, quote(c.Title), quote(c.Description+" Evidence: "+evidence+"; contract "+c.Version))
	}
	for i, c := range p.System.Consumers {
		w("  cluster%d = cluster %s { description %s }\n", i, quote(c.Title), quote("CAPI consumer identity: "+c.ID+"; topology context only."))
	}
	for _, e := range m.Edges {
		from, to := strings.Split(e.From, "."), strings.Split(e.To, ".")
		w("  %s -> %s %s { description %s }\n", from[0], to[0], quote(from[1]+" → "+to[1]+" / "+e.Protocol), quote(e.From+" → "+e.To+"; state "+e.State+"; "+e.Evidence))
	}
	for i, c := range p.System.Consumers {
		w("  infra -> cluster%d %s\n", i, quote("consumer "+c.ID))
	}
	var include []string
	for _, c := range p.System.Components {
		include = append(include, c.ID)
	}
	for i := range p.System.Consumers {
		include = append(include, fmt.Sprintf("cluster%d", i))
	}
	w("}\nviews {\n  view index {\n    title 'MAC and InfraMachine: resolved contracts'\n    include %s\n    autoLayout TopBottom\n  }\n}\n", strings.Join(include, ", "))
	return b.String()
}
func (m *Model) Generate(updateLock bool) error {
	p := m.Project
	lockPath := filepath.Join(p.Root, "contracts.lock.yaml")
	if !updateLock {
		var lock map[string]LockEntry
		if err := ReadResource(lockPath, "ContractLock", &lock); err != nil {
			return err
		}
		if !reflect.DeepEqual(lock, p.Lock) {
			return fmt.Errorf("contract drift: review changes then run arch generate --update-lock")
		}
	}
	dir := filepath.Join(p.Root, "generated")
	adapter, err := m.AdapterGo()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	quint := m.Quint()
	hash := digest([]byte(quint))
	old, _ := os.ReadFile(filepath.Join(dir, "System.qnt"))
	if digest(old) != hash {
		os.Remove(filepath.Join(dir, "revocation-race.c4"))
		os.Remove(filepath.Join(p.Root, "reports/summary.yaml"))
		os.Remove(filepath.Join(p.Root, "reports/controller-demo.yaml"))
	}
	if updateLock {
		if err := WriteResource(lockPath, "ContractLock", "contracts", p.Lock); err != nil {
			return err
		}
	}
	for name, text := range map[string]string{"System.qnt": quint, "architecture.c4": m.LikeC4(), "adapter_generated.go": string(adapter)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0644); err != nil {
			return err
		}
	}
	if p.Sources != nil {
		if err := os.WriteFile(filepath.Join(dir, "source-structure.c4"), []byte(p.Sources.LikeC4()), 0644); err != nil {
			return err
		}
	}
	sys, err := os.ReadFile(filepath.Join(p.Root, "system.yaml"))
	if err != nil {
		return err
	}
	scenarios, err := os.ReadFile(filepath.Join(p.Root, "scenarios.yaml"))
	if err != nil {
		return err
	}
	ir := map[string]any{"implementation": "Go / cel-go", "system": p.System, "model": m, "sourceDescriptors": p.Sources, "provenance": map[string]any{"contracts": p.Lock, "systemSha256": digest(sys), "scenariosSha256": digest(scenarios)}}
	if err := WriteResult(filepath.Join(dir, "architecture.yaml"), "Architecture", "machine-vendor", ir); err != nil {
		return err
	}
	actions := []map[string]any{}
	for _, a := range m.Actions {
		actions = append(actions, map[string]any{"id": a.ID, "params": a.Params, "owner": a.Owner, "quint": a.Quint})
	}
	projection := []map[string]string{}
	for _, s := range m.States {
		projection = append(projection, map[string]string{"variable": s.Name, "owner": s.Owner})
	}
	return WriteResult(filepath.Join(dir, "mbt-bindings.yaml"), "TestBindings", "machine-vendor", map[string]any{"modelSha256": hash, "hosts": p.System.Hosts, "infraMachines": p.System.InfraMachines, "maxEpoch": p.System.MaxEpoch, "actions": actions, "projection": projection, "adapterInterface": "generated/adapter_generated.go", "evidence": "Generated wiring only; see reports for independently executed simulator and demo controller results. No upstream conformance."})
}

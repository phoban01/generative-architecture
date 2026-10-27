package composition

import (
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/protocol"
	"strconv"
	"strings"
)

// ProtocolCheck reports one protocol generated into a composed model. The
// listed properties check conformance to the protocol. They are separate
// from the binding: a link that composes is not evidence that its sides
// follow the protocol.
type ProtocolCheck struct {
	Link       Link     `yaml:"link"`
	Protocol   string   `yaml:"protocol"`
	Version    string   `yaml:"version"`
	State      string   `yaml:"state"`
	Properties []string `yaml:"properties"`
}

// LinkID is the stable identifier of a link in generated names. It derives
// from the bound component and port names, never from positions.
func LinkID(l Link) string {
	return identifier(strings.ReplaceAll(l.From, ".", "_") + "__" + strings.ReplaceAll(l.To, ".", "_"))
}

// attachProtocols adds, for each link that names a protocol, a monitor
// variable <link>_protoState. Every composed action of the provider or
// consumer component that writes the bound state moves the monitor along the
// first permitted transition of its role, or to protocol.Violation when none
// permits the write. The generated property <link>_conforms says the monitor
// never reaches Violation. The contract's own properties follow, named
// <link>_<property>, over the monitor state and the bound state.
func attachProtocols(result *Result, links []Link, ports map[string]Port, owners map[string]string, protocols map[string]protocol.Contract) error {
	for i, l := range links {
		if l.Protocol == "" {
			continue
		}
		at := fmt.Sprintf("link %s -> %s: protocol %s", l.From, l.To, l.Protocol)
		c, ok := protocols[l.Protocol]
		if !ok {
			return fmt.Errorf("%s: contract not loaded", at)
		}
		if err := c.Validate(); err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		provider := strings.SplitN(l.From, ".", 2)[0]
		consumer := strings.SplitN(l.To, ".", 2)[0]
		if provider == consumer {
			return fmt.Errorf("%s: provider and consumer are the same component", at)
		}
		from, to := ports[l.From], ports[l.To]
		for j, t := range c.Transitions {
			if t.Actor == protocol.Consumer && !to.Writable {
				return fmt.Errorf("%s: roles do not match port directions: transitions[%d] %s is a consumer write, but in port %s is read-only", at, j, t.Name, l.To)
			}
		}
		shared := result.StateNames[provider+"."+from.State]
		id := LinkID(l)
		monitor := id + "_protoState"
		if _, taken := result.Model.State[monitor]; taken {
			return fmt.Errorf("%s: generated state %s collides with existing state", at, monitor)
		}
		for _, other := range links[:i] {
			if other.Protocol != "" && LinkID(other) == id {
				return fmt.Errorf("%s: link identifier %s is not unique", at, id)
			}
		}
		result.Model.State[monitor] = model.Variable{Type: "string", Initial: expr.Expression(strconv.Quote(c.Initial))}
		for k := range result.Model.Actions {
			a := &result.Model.Actions[k]
			next, writes := a.Updates[shared]
			role := ""
			switch owners[a.Name] {
			case provider:
				role = protocol.Provider
			case consumer:
				role = protocol.Consumer
			}
			if !writes || role == "" {
				continue
			}
			step := expr.Expression(strconv.Quote(protocol.Violation))
			for j := len(c.Transitions) - 1; j >= 0; j-- {
				t := c.Transitions[j]
				if t.Actor != role {
					continue
				}
				guard := t.Guard
				if guard == "" {
					guard = "true"
				}
				g, err := expr.Rewrite(guard, map[string]expr.Expression{"port": expr.Expression(shared), "next": next, "state": expr.Expression(monitor)})
				if err != nil {
					return fmt.Errorf("%s: transitions[%d].guard: %w", at, j, err)
				}
				step = expr.Expression(fmt.Sprintf("%s == %q && (%s) ? %q : (%s)", monitor, t.From, g, t.To, step))
			}
			a.Updates[monitor] = step
		}
		check := ProtocolCheck{Link: l, Protocol: c.Name, Version: c.Version, State: monitor}
		conforms := model.Property{Name: id + "_conforms", Expression: expr.Expression(fmt.Sprintf("%s != %q", monitor, protocol.Violation))}
		result.Model.Properties = append(result.Model.Properties, conforms)
		check.Properties = append(check.Properties, conforms.Name)
		for j, p := range c.Properties {
			v, err := expr.Rewrite(p.Expression, map[string]expr.Expression{"port": expr.Expression(shared), "state": expr.Expression(monitor)})
			if err != nil {
				return fmt.Errorf("%s: properties[%d]: %w", at, j, err)
			}
			name := id + "_" + p.Name
			result.Model.Properties = append(result.Model.Properties, model.Property{Name: name, Expression: v})
			check.Properties = append(check.Properties, name)
		}
		result.Protocols = append(result.Protocols, check)
	}
	return nil
}

package controllerruntime

import (
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/model"
	"strconv"
	"strings"
)

type ModelKey struct {
	ID        string `yaml:"id"`
	Namespace string `yaml:"namespace"`
	Name      string `yaml:"name"`
}
type ModelController struct {
	Name string `yaml:"name"`
	// Queue overrides the profile's queue kind for this controller.
	Queue      string     `yaml:"queue,omitempty"`
	Workers    int        `yaml:"workers"`
	RetryTicks int        `yaml:"retryTicks"`
	AfterTicks int        `yaml:"afterTicks"`
	Keys       []ModelKey `yaml:"keys"`
	// Startup models the initial list and the cache sync: no key starts
	// before the controller's caches sync.
	Startup bool `yaml:"startup,omitempty"`
	// Deletion models delete events, including tombstones.
	Deletion bool `yaml:"deletion,omitempty"`
}

// Queue kinds. QueueFIFO is client-go's rate-limited FIFO work queue;
// QueuePriority is controller-runtime's priority queue.
const (
	QueueFIFO     = "fifo"
	QueuePriority = "priority"
)

type GroupProfile struct {
	Name     string `yaml:"name"`
	MaxTicks int    `yaml:"maxTicks"`
	// Queue selects the queue kind of every controller: fifo (the default)
	// or priority. A controller's own Queue field overrides it.
	Queue       string            `yaml:"queue,omitempty"`
	Controllers []ModelController `yaml:"controllers"`
}

// QueueOf resolves the queue kind of c: its own field, then the profile's,
// then fifo. It rejects unknown kinds.
func (p GroupProfile) QueueOf(c ModelController) (string, error) {
	q := c.Queue
	if q == "" {
		q = p.Queue
	}
	if q == "" {
		q = QueueFIFO
	}
	if q != QueueFIFO && q != QueuePriority {
		return "", fmt.Errorf("controller %s: unknown queue %q (want fifo or priority)", c.Name, q)
	}
	return q, nil
}

type RuntimeEvent struct{ Controller, Key, Kind, Outcome string }
type GroupModel struct {
	Spec    model.Spec
	Events  map[string]RuntimeEvent
	Profile GroupProfile
}

// BuildGroupModel creates the bounded runtime model from the same controller/key
// domain used by Group. Queue semantics are reviewed library summaries.
func BuildGroupModel(p GroupProfile) (GroupModel, error) {
	out := GroupModel{Spec: model.Spec{Name: p.Name, State: map[string]model.Variable{"now": {Type: "int", Initial: "0"}}}, Events: map[string]RuntimeEvent{}, Profile: p}
	if p.MaxTicks < 1 || len(p.Controllers) == 0 {
		return out, fmt.Errorf("positive time bound and controllers required")
	}
	add := func(name string, event RuntimeEvent, guard string, u map[string]expr.Expression) {
		out.Spec.Actions = append(out.Spec.Actions, model.Action{Name: name, Guard: expr.Expression(guard), Updates: u})
		out.Events[name] = event
	}
	add("advance", RuntimeEvent{Kind: "advance"}, fmt.Sprintf("now < %d", p.MaxTicks), map[string]expr.Expression{"now": "now + 1"})
	controllers := map[string]bool{}
	for _, c := range p.Controllers {
		if c.Name == "" || controllers[c.Name] || c.Workers < 1 || c.RetryTicks < 1 || c.AfterTicks < 1 || len(c.Keys) == 0 {
			return out, fmt.Errorf("invalid controller model profile")
		}
		controllers[c.Name] = true
		queue, err := p.QueueOf(c)
		if err != nil {
			return out, err
		}
		if queue == QueuePriority {
			if err := addPriorityController(add, &out.Spec, c); err != nil {
				return out, err
			}
			continue
		}
		next := c.Name + "_next"
		out.Spec.State[next] = model.Variable{Type: "int", Initial: "0"}
		keys := map[string]bool{}
		objects := map[string]bool{}
		active := []string{}
		for _, k := range c.Keys {
			if k.ID == "" || k.Name == "" || keys[k.ID] || objects[k.Namespace+"/"+k.Name] {
				return out, fmt.Errorf("invalid/duplicate model key")
			}
			keys[k.ID] = true
			objects[k.Namespace+"/"+k.Name] = true
			prefix := c.Name + "_" + k.ID
			for _, v := range []string{"ready", "processing", "dirty"} {
				out.Spec.State[prefix+"_"+v] = model.Variable{Type: "bool", Initial: "false"}
			}
			for _, v := range []string{"retries", "order"} {
				out.Spec.State[prefix+"_"+v] = model.Variable{Type: "int", Initial: "0"}
			}
			out.Spec.State[prefix+"_due"] = model.Variable{Type: "int", Initial: "-1"}
			active = append(active, "("+prefix+"_processing ? 1 : 0)")
		}
		for _, k := range c.Keys {
			prefix := c.Name + "_" + k.ID
			ready, processing, dirty, due, retries, order := prefix+"_ready", prefix+"_processing", prefix+"_dirty", prefix+"_due", prefix+"_retries", prefix+"_order"
			enqueue := func() map[string]expr.Expression {
				return map[string]expr.Expression{dirty: "true", ready: expr.Expression(ready + " || !" + processing), order: expr.Expression(fmt.Sprintf("!%s && !%s ? %s : %s", ready, processing, next, order)), next: expr.Expression(fmt.Sprintf("!%s && !%s ? %s + 1 : %s", ready, processing, next, next))}
			}
			add(prefix+"_enqueue", RuntimeEvent{c.Name, k.ID, "enqueue", ""}, "true", enqueue())
			guard := ready + " && !" + processing + fmt.Sprintf(" && (%s) < %d", strings.Join(active, " + "), c.Workers)
			for _, other := range c.Keys {
				if other.ID != k.ID {
					op := c.Name + "_" + other.ID
					guard += " && (!" + op + "_ready || " + order + " < " + op + "_order)"
				}
			}
			add(prefix+"_start", RuntimeEvent{c.Name, k.ID, "start", ""}, guard, map[string]expr.Expression{ready: "false", dirty: "false", processing: "true"})
			fire := enqueue()
			fire[due] = "-1"
			add(prefix+"_fire", RuntimeEvent{c.Name, k.ID, "fire", ""}, due+" >= 0 && now >= "+due, fire)
			for _, outcome := range []string{"success", "error", "requeue", "after", "terminal", "errorAfter"} {
				u := map[string]expr.Expression{processing: "false", ready: expr.Expression(dirty), order: expr.Expression(dirty + " ? " + next + " : " + order), next: expr.Expression(dirty + " ? " + next + " + 1 : " + next)}
				if outcome == "error" || outcome == "requeue" || outcome == "errorAfter" {
					u[retries] = expr.Expression(retries + " + 1")
				} else if outcome != "terminal" {
					u[retries] = "0"
				}
				delay := 0
				if outcome == "error" || outcome == "requeue" || outcome == "errorAfter" {
					delay = c.RetryTicks
				}
				if outcome == "after" {
					delay = c.AfterTicks
				}
				if delay > 0 {
					u[due] = expr.Expression(fmt.Sprintf("%s < 0 || now + %d < %s ? now + %d : %s", due, delay, due, delay, due))
				}
				add(prefix+"_"+outcome, RuntimeEvent{c.Name, k.ID, "finish", outcome}, processing, u)
			}
			out.Spec.Properties = append(out.Spec.Properties, model.Property{Name: prefix + "_exclusive", Expression: expr.Expression("!(" + ready + " && " + processing + ") && (!" + ready + " || " + dirty + ")")})
		}
		out.Spec.Properties = append(out.Spec.Properties, model.Property{Name: c.Name + "_capacity", Expression: expr.Expression("(" + strings.Join(active, " + ") + ") <= " + strconv.Itoa(c.Workers))})
	}
	for _, c := range p.Controllers {
		addLifecycle(add, &out.Spec, c)
	}
	_, err := model.Compile(out.Spec)
	return out, err
}

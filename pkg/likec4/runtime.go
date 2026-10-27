package likec4

import (
	"fmt"

	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

// queueKind describes the queue every controller of a group profile runs:
// the controller-runtime workqueue the runtime model summarizes.
const queueKind = "rate-limited FIFO workqueue, one entry per key"

// RuntimeView draws a controller-runtime group profile: a queue element per
// controller with its workers, retry and requeue delays and queue kind, the
// controller it feeds, and a resource element per reconcile key. Keys
// enqueue into the queue and the workers dequeue into the controller. The
// profile is declared, never extracted, so everything carries declared
// evidence and the source of the profile, when given.
func RuntimeView(name string, p cr.GroupProfile, source ir.Source) ([]Element, []Relation, View, error) {
	view := View{Name: ID(name), Title: "Runtime: " + p.Name}
	var elements []Element
	var relations []Relation
	add := func(e Element) {
		e.Evidence, e.Source = ir.Declared, source
		elements = append(elements, e)
		view.Include = append(view.Include, e.ID)
	}
	seen := map[string]bool{}
	for _, c := range p.Controllers {
		if c.Name == "" || seen[c.Name] {
			return nil, nil, View{}, fmt.Errorf("profile %s: controller name %q empty or repeated", p.Name, c.Name)
		}
		seen[c.Name] = true
		if c.Workers < 1 {
			return nil, nil, View{}, fmt.Errorf("profile %s: controller %s has no workers", p.Name, c.Name)
		}
		queue, controller := ID("queue_"+c.Name), ID("runtime_"+c.Name)
		add(Element{ID: queue, Kind: KindQueue, Title: c.Name + " queue", Description: fmt.Sprintf("%s; workers %d; retry after %d ticks; requeue after %d ticks; clock bound %d ticks", queueKind, c.Workers, c.RetryTicks, c.AfterTicks, p.MaxTicks)})
		add(Element{ID: controller, Kind: KindController, Title: c.Name, Description: "reconciler of group " + p.Name})
		relations = append(relations, Relation{From: queue, To: controller, Label: "dequeue", Description: fmt.Sprintf("%d workers", c.Workers), Evidence: ir.Declared, Source: source})
		for _, k := range c.Keys {
			key := ID("key_" + c.Name + "_" + k.ID)
			title := k.Name
			if k.Namespace != "" {
				title = k.Namespace + "/" + k.Name
			}
			add(Element{ID: key, Kind: KindResource, Title: title, Description: "reconcile key " + k.ID})
			relations = append(relations, Relation{From: key, To: queue, Label: "enqueue", Description: "events on " + title, Evidence: ir.Declared, Source: source})
		}
	}
	return elements, relations, view, nil
}

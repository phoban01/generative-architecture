package controllerruntime

import (
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/model"
	"maps"
)

// addLifecycle adds the startup and deletion events of controller c to a
// model that already holds its queue actions. It follows controller-runtime
// v0.23.3 (pkg/internal/controller/controller.go and pkg/handler):
//
//   - Startup: the informer lists every existing object and delivers each as
//     an add from the initial list, in no fixed order. A priority queue gets
//     these adds at LowPriority; a FIFO queue gets plain adds. The controller
//     waits for its sources to sync before it starts workers. So a
//     <controller>_<key>_list action adds one key before sync, a
//     <controller>_sync action marks the caches synced, and every start
//     waits for sync. A key may also be absent from the list.
//   - Deletion: a delete event, or a tombstone for a delete the informer
//     missed, enqueues the key at normal priority once more.
//     <controller>_<key>_tombstone sets the key's deleted flag and enqueues
//     it. A key is deleted at most once.
func addLifecycle(add func(string, RuntimeEvent, string, map[string]expr.Expression), spec *model.Spec, c ModelController) {
	if !c.Startup && !c.Deletion {
		return
	}
	actions := map[string]int{}
	for i, a := range spec.Actions {
		actions[a.Name] = i
	}
	updates := func(name string) map[string]expr.Expression {
		return maps.Clone(spec.Actions[actions[name]].Updates)
	}
	synced := c.Name + "_synced"
	if c.Startup {
		spec.State[synced] = model.Variable{Type: "bool", Initial: "false"}
		for _, k := range c.Keys {
			prefix := c.Name + "_" + k.ID
			start := &spec.Actions[actions[prefix+"_start"]]
			start.Guard = expr.Expression(synced + " && " + string(start.Guard))
			low := prefix + "_enqueue"
			if _, ok := actions[prefix+"_enqueueLow"]; ok {
				low = prefix + "_enqueueLow"
			}
			add(prefix+"_list", RuntimeEvent{c.Name, k.ID, "list", ""}, "!"+synced, updates(low))
		}
		add(c.Name+"_sync", RuntimeEvent{Controller: c.Name, Kind: "sync"}, "!"+synced, map[string]expr.Expression{synced: "true"})
	}
	if c.Deletion {
		for _, k := range c.Keys {
			prefix := c.Name + "_" + k.ID
			deleted := prefix + "_deleted"
			spec.State[deleted] = model.Variable{Type: "bool", Initial: "false"}
			u := updates(prefix + "_enqueue")
			u[deleted] = "true"
			add(prefix+"_tombstone", RuntimeEvent{c.Name, k.ID, "tombstone", ""}, "!"+deleted, u)
		}
	}
}

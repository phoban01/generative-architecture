package database

import (
	apiv1 "github.com/phoban01/generative-architecture/examples/database/api/v1alpha1"
	"github.com/phoban01/generative-architecture/examples/database/generated"
	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/watchgen"
	"github.com/phoban01/generative-architecture/pkg/watchmodel"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// This file is the adapter's side of the watch model: the native FIFO the
// generated delivery feeds and the native handlers of the extracted
// mapping functions. The delivery itself is generated (watches_generated.go).

var databaseKey = client.ObjectKey{Namespace: "demo", Name: "db-1"}

// gvk returns the GVK of kind in the sample's API group.
func gvk(kind string) ir.GVK {
	return ir.GVK{Group: "demo.architecture.local", Version: "v1alpha1", Kind: kind}
}

// The watch model's delivery actions, which the schedules address.
var (
	deliverDatabase = watchmodel.DeliverAction(gvk("Database"))
	deliverInstance = watchmodel.DeliverAction(gvk("DatabaseInstance"))
)

// eventQueue wraps an actual FIFO. The notification bit is observational
// bookkeeping; native Get/Done/Len validate deduplication and processing behaviour.
type eventQueue struct {
	worker *cr.Worker
	queued bool
	err    error
}

func (q *eventQueue) Len() int {
	if q.worker.Ready() {
		return 1
	}
	return 0
}
func (q *eventQueue) Deliver(deliver func(workqueue.TypedRateLimitingInterface[reconcile.Request])) {
	keys, err := q.worker.EnqueueFrom(deliver)
	if len(keys) > 0 {
		q.queued = true
	}
	if err != nil {
		q.err = err
	}
}

// Queued reports whether a delivery queued the key since the last begin.
func (q *eventQueue) Queued() bool { return q.queued }

// Err is the first delivery outside the declared keys.
func (q *eventQueue) Err() error { return q.err }

// watchHandlers supplies the native handler of each mapping function the
// extracted rules name, for the generated delivery.
func watchHandlers() map[string]handler.EventHandler {
	return map[string]handler.EventHandler{"InstanceToDatabase": handler.EnqueueRequestsFromMapFunc(InstanceToDatabase)}
}

// watchTypes supplies the empty typed object of each kind a typed rule
// delivers, for the generated delivery.
func watchTypes() map[string]func() client.Object {
	return map[string]func() client.Object{"DatabaseInstance": func() client.Object { return &apiv1.DatabaseInstance{} }}
}

// watchRules converts the extracted rules on kind to the generated
// delivery's rules, so a changed wiring is delivered as extracted.
func watchRules(w watchgen.Result, kind string) []generated.WatchRule {
	var out []generated.WatchRule
	for _, rule := range w.Rules {
		if rule.Resource.Kind == kind {
			out = append(out, generated.WatchRule{Mapping: rule.Mapping, Unstructured: rule.Unstructured, Predicates: rule.Predicates})
		}
	}
	return out
}

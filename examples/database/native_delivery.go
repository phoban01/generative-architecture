package database

import (
	"fmt"
	"strconv"

	apiv1 "generative-architecture/examples/database/api/v1alpha1"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/ir"
	"generative-architecture/pkg/watchgen"
	"generative-architecture/pkg/watchmodel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// This file is the adapter's side of the watch model: it passes each event
// through the native controller-runtime predicates and handlers into a
// native FIFO. Adapter generation (M5) replaces it.

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

// watchedObject is the informer's view of one object: its undelivered
// event, and the generation, resource version and reference of the object
// now and when it was last delivered. The last delivered copy is the
// ObjectOld of the next update.
type watchedObject struct {
	gvk                                 ir.GVK
	event                               string
	pending                             bool
	generation, resourceVersion         int64
	generationSeen, resourceVersionSeen int64
	// reference and referenceSeen hold an instance's spec.databaseName.
	reference, referenceSeen string
}

// newWatchedObjects returns the informer view of the Database and of its
// instance. The instance always names db-1: CreateDatabaseInstance writes
// that reference and nothing changes it.
func newWatchedObjects() map[string]*watchedObject {
	return map[string]*watchedObject{
		"Database":         {gvk: gvk("Database")},
		"DatabaseInstance": {gvk: gvk("DatabaseInstance"), reference: databaseKey.Name, referenceSeen: databaseKey.Name},
	}
}

// record notes a change of the object of kind, as the API server moves its
// resource version and, on a create or a spec change, its generation. An
// update behind an undelivered create waits; other events coalesce.
func (a *Adapter) record(kind, ev string, generation bool) {
	o := a.objects[kind]
	o.resourceVersion++
	switch ev {
	case watchmodel.Create:
		o.event, o.pending = ev, false
		o.generation++
		o.generationSeen, o.resourceVersionSeen = o.generation, o.resourceVersion
	case watchmodel.Delete:
		o.event, o.pending = ev, false
	case watchmodel.Update:
		o.pending = o.pending || o.event == watchmodel.Create
		if o.event == "" {
			o.event = ev
		}
		if generation {
			o.generation++
		}
	}
}

// observeWatches returns the watch state under the model's names.
func (a *Adapter) observeWatches(into map[string]any) {
	key := ir.ObjectRef{GVK: a.objects["Database"].gvk, Namespace: databaseKey.Namespace, Name: databaseKey.Name}
	into[watchmodel.QueuedVar(key)] = a.queue.queued
	into[watchmodel.WrongKeyVar] = a.queue.err != nil
	for _, o := range a.objects {
		into[watchmodel.EventVar(o.gvk)] = o.event
		into[watchmodel.PendingVar(o.gvk)] = o.pending
		into[watchmodel.GenerationVar(o.gvk)] = o.generation
		into[seenVar(watchmodel.GenerationVar(o.gvk))] = o.generationSeen
		into[watchmodel.ResourceVersionVar(o.gvk)] = o.resourceVersion
		into[seenVar(watchmodel.ResourceVersionVar(o.gvk))] = o.resourceVersionSeen
	}
	instance := a.objects["DatabaseInstance"]
	reference := watchmodel.FieldVar(instance.gvk, []string{"Spec", "DatabaseName"})
	into[reference] = instance.reference
	into[seenVar(reference)] = instance.referenceSeen
}

// seenVar names the last delivered copy of v, as watchmodel does.
func seenVar(v string) string { return v + "Seen" }

// deliver hands the event of the object of kind to every extracted watch
// rule on kind, through the native predicates and handlers.
func (a *Adapter) deliver(kind string) error {
	o := a.objects[kind]
	if o.event == "" {
		return fmt.Errorf("no %s event to deliver", kind)
	}
	for _, rule := range a.watches.Rules {
		if rule.Resource.Kind != kind {
			continue
		}
		obj, old, err := o.objects(rule)
		if err != nil {
			return err
		}
		h := handler.EventHandler(&handler.EnqueueRequestForObject{})
		if kind == "DatabaseInstance" {
			h = handler.EnqueueRequestsFromMapFunc(InstanceToDatabase)
		}
		allow := true
		for _, name := range rule.Predicates {
			var p predicate.Predicate
			switch name {
			case "generationChanged":
				p = predicate.GenerationChangedPredicate{}
			case "resourceVersionChanged":
				p = predicate.ResourceVersionChangedPredicate{}
			default:
				return fmt.Errorf("unsupported predicate %s", name)
			}
			switch o.event {
			case watchmodel.Create:
				allow = allow && p.Create(event.CreateEvent{Object: obj})
			case watchmodel.Update:
				allow = allow && p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: obj})
			case watchmodel.Delete:
				allow = allow && p.Delete(event.DeleteEvent{Object: obj})
			}
		}
		if !allow {
			continue
		}
		switch o.event {
		case watchmodel.Create:
			a.queue.Deliver(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				h.Create(a.ctx, event.CreateEvent{Object: obj}, q)
			})
		case watchmodel.Update:
			a.queue.Deliver(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				h.Update(a.ctx, event.UpdateEvent{ObjectOld: old, ObjectNew: obj}, q)
			})
		case watchmodel.Delete:
			a.queue.Deliver(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				h.Delete(a.ctx, event.DeleteEvent{Object: obj}, q)
			})
		default:
			return fmt.Errorf("unsupported event %s", o.event)
		}
	}
	// A delivered create keeps the copy taken at creation, so that an
	// update behind it compares against the created object.
	if o.event != watchmodel.Create {
		o.generationSeen, o.resourceVersionSeen, o.referenceSeen = o.generation, o.resourceVersion, o.reference
	}
	o.event = ""
	if o.pending {
		o.event = watchmodel.Update
	}
	o.pending = false
	return a.queue.err
}

// objects builds the object an event carries and the last delivered copy,
// typed or unstructured as rule watches them.
func (o *watchedObject) objects(rule watchgen.Rule) (client.Object, client.Object, error) {
	build := func(generation, resourceVersion int64, reference string) (client.Object, error) {
		var obj client.Object
		if o.gvk.Kind == "Database" {
			obj = &apiv1.Database{ObjectMeta: metav1.ObjectMeta{Namespace: databaseKey.Namespace, Name: databaseKey.Name}}
		} else {
			obj = &apiv1.DatabaseInstance{ObjectMeta: metav1.ObjectMeta{Namespace: databaseKey.Namespace, Name: "instance-for-" + databaseKey.Name}, Spec: apiv1.DatabaseInstanceSpec{DatabaseName: reference}}
		}
		obj.SetGeneration(generation)
		obj.SetResourceVersion(strconv.FormatInt(resourceVersion, 10))
		if !rule.Unstructured {
			return obj, nil
		}
		fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return nil, err
		}
		u := &unstructured.Unstructured{Object: fields}
		u.SetGroupVersionKind(schema.GroupVersionKind{Group: rule.Resource.Group, Version: rule.Resource.Version, Kind: rule.Resource.Kind})
		return u, nil
	}
	obj, err := build(o.generation, o.resourceVersion, o.reference)
	if err != nil {
		return nil, nil, err
	}
	old, err := build(o.generationSeen, o.resourceVersionSeen, o.referenceSeen)
	return obj, old, err
}

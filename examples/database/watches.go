package database

import (
	"fmt"
	apiv1 "generative-architecture/examples/database/api/v1alpha1"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/watchgen"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
)

var databaseKey = client.ObjectKey{Namespace: "demo", Name: "db-1"}

func extractWatches(source []byte) (watchgen.Result, error) {
	resources := map[string]effect.Resource{}
	for _, kind := range []string{"Database", "DatabaseInstance"} {
		resources["generative-architecture/examples/database/api/v1alpha1."+kind] = effect.Resource{Group: "demo.architecture.local", Version: "v1alpha1", Kind: kind}
	}
	watches, err := watchgen.Extract(source, "DatabaseReconciler", "SetupWithManager", resources)
	if err != nil {
		return watches, err
	}
	for _, r := range watches.Rules {
		if r.Mapping == "self" {
			if r.Resource.Kind != "Database" {
				return watches, fmt.Errorf("unsupported primary resource")
			}
			continue
		}
		if r.Mapping != "InstanceToDatabase" || r.Resource.Kind != "DatabaseInstance" {
			return watches, fmt.Errorf("unsummarized mapping %s", r.Mapping)
		}

	}
	return watches, nil
}

// addWatchModel composes the extracted registrations with a reviewed one-key FIFO
// summary. Events may be delivered while processing; queued then means a dirty key.
// The generated environment actions raise the event of their object's kind:
// creating the Database raises a create event, and an update of the
// instance raises an update that coalesces with an undelivered create.
func addWatchModel(spec model.Spec, w watchgen.Result, env []EnvironmentAction) (model.Spec, error) {
	for _, name := range []string{"queued", "wrongKey", "instanceUpdatePending"} {
		spec.State[name] = model.Variable{Type: "bool", Initial: "false"}
	}
	for _, name := range []string{"databaseEvent", "instanceEvent"} {
		spec.State[name] = model.Variable{Type: "string", Initial: `""`}
	}
	environment := map[string]EnvironmentAction{}
	for _, e := range env {
		environment[e.Name] = e
	}
	for i := range spec.Actions {
		a := &spec.Actions[i]
		if e, ok := environment[a.Name]; ok {
			switch {
			case e.Kind == "Database" && e.Operation == EnvCreate:
				a.Updates["databaseEvent"] = `"create"`
			case e.Kind == "DatabaseInstance" && e.Operation == EnvUpdate:
				a.Updates["instanceEvent"] = `instanceEvent == "create" ? "create" : "update"`
				a.Updates["instanceUpdatePending"] = `instanceEvent == "create" || instanceUpdatePending`
			default:
				return spec, fmt.Errorf("environment action %s: no watch event for %s of %s", e.Name, e.Operation, e.Kind)
			}
		}
		if a.Name == "reconcile" {
			a.Guard = "(" + a.Guard + ") && queued"
			a.Updates["queued"] = "false"
		}
		if a.Effect == "createInstance" {
			a.Updates["instanceEvent"] = `"create"`
		}
		if a.Effect == "publish" {
			a.Updates["databaseEvent"] = `"update"`
		}
	}
	for _, r := range []struct{ event, kind, action string }{{"databaseEvent", "Database", "deliverDatabase"}, {"instanceEvent", "DatabaseInstance", "deliverInstance"}} {
		queued, wrong := expr.Expression("queued"), expr.Expression("wrongKey")
		conditions := []string{}
		foreign := []string{}
		for _, rule := range w.Rules {
			if rule.Resource.Kind != r.kind {
				continue
			}
			condition := "true"
			for _, p := range rule.Predicates {
				if p == "generationChanged" {
					condition = r.event + ` != "update"`
				}
			}
			target, err := mappingTarget(rule)
			if err != nil {
				return spec, err
			}
			if target == databaseKey {
				conditions = append(conditions, condition)
			} else {
				foreign = append(foreign, condition)
			}
		}
		if len(conditions) > 0 {
			queued = expr.Expression("queued || (" + strings.Join(conditions, " || ") + ")")
		}
		if len(foreign) > 0 {
			wrong = expr.Expression("wrongKey || (" + strings.Join(foreign, " || ") + ")")
		}
		updates := map[string]expr.Expression{r.event: `""`, "queued": queued, "wrongKey": wrong}
		if r.kind == "DatabaseInstance" {
			updates[r.event] = `instanceEvent == "create" && instanceUpdatePending ? "update" : ""`
			updates["instanceUpdatePending"] = "false"
		}
		spec.Actions = append(spec.Actions, model.Action{Name: r.action, Guard: expr.Expression(r.event + ` != ""`), Updates: updates})
	}
	// A delayed event can refer to an object already gone by reconciliation time.
	spec.Actions = append(spec.Actions, model.Action{Name: "removeDatabaseBeforeRead", Guard: expr.Expression(databaseExistsVariable + " && !" + instanceExistsVariable + " && invocations == 0"), Updates: map[string]expr.Expression{databaseExistsVariable: "false", contractVariable: "false", databaseEndpointVariable: `""`, "databaseEvent": `"delete"`}})
	return spec, nil
}

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
func (a *Adapter) deliver(kind string) error {
	pending := a.databaseEvent
	if kind == "DatabaseInstance" {
		pending = a.instanceEvent
	}
	if pending == "" {
		return fmt.Errorf("no %s event to deliver", kind)
	}
	for _, rule := range a.watches.Rules {
		if rule.Resource.Kind != kind {
			continue
		}
		var h handler.EventHandler
		var obj client.Object
		if kind == "Database" {
			h = &handler.EnqueueRequestForObject{}
			obj = &apiv1.Database{ObjectMeta: metav1.ObjectMeta{Namespace: databaseKey.Namespace, Name: databaseKey.Name}, Status: apiv1.DatabaseStatus{Endpoint: a.statusEndpoint}}
		} else {
			h = handler.EnqueueRequestsFromMapFunc(InstanceToDatabase)
			obj = &apiv1.DatabaseInstance{ObjectMeta: metav1.ObjectMeta{Namespace: databaseKey.Namespace, Name: "instance-for-db-1"}, Spec: apiv1.DatabaseInstanceSpec{DatabaseName: databaseKey.Name}, Status: apiv1.DatabaseInstanceStatus{Endpoint: a.instanceEndpoint}}
		}
		if rule.Unstructured {
			fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
			if err != nil {
				return err
			}
			u := &unstructured.Unstructured{Object: fields}
			u.SetGroupVersionKind(schema.GroupVersionKind{Group: rule.Resource.Group, Version: rule.Resource.Version, Kind: rule.Resource.Kind})
			obj = u
		}
		obj.SetGeneration(1)
		obj.SetResourceVersion("2")
		old := obj.DeepCopyObject().(client.Object)
		old.SetResourceVersion("1")
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
			switch pending {
			case "create":
				allow = allow && p.Create(event.CreateEvent{Object: obj})
			case "update":
				allow = allow && p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: obj})
			case "delete":
				allow = allow && p.Delete(event.DeleteEvent{Object: obj})
			}
		}
		if !allow {
			continue
		}
		switch pending {
		case "create":
			a.queue.Deliver(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				h.Create(a.ctx, event.CreateEvent{Object: obj}, q)
			})
		case "update":
			a.queue.Deliver(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				h.Update(a.ctx, event.UpdateEvent{ObjectOld: old, ObjectNew: obj}, q)
			})
		case "delete":
			a.queue.Deliver(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				h.Delete(a.ctx, event.DeleteEvent{Object: obj}, q)
			})
		default:
			return fmt.Errorf("unsupported event %s", pending)
		}
	}
	if kind == "DatabaseInstance" {
		if a.instanceEvent == "create" && a.instanceUpdatePending {
			a.instanceEvent = "update"
		} else {
			a.instanceEvent = ""
		}
		a.instanceUpdatePending = false
	} else {
		a.databaseEvent = ""
	}
	return a.queue.err
}
func (e Example) WatchLikeC4() string {
	var b strings.Builder
	fmt.Fprintln(&b, "specification { element databaseWatchPart }\nmodel {")
	fmt.Fprintln(&b, ` db_watch_queue = databaseWatchPart "Database controller workqueue"
 db_watch_reconcile = databaseWatchPart "Database controller reconcile"
 db_watch_queue -> db_watch_reconcile "dequeue Database key"`)
	names := []string{"db_watch_queue", "db_watch_reconcile"}
	for i, r := range e.Watches.Rules {
		name := fmt.Sprintf("db_watch_source%d", i)
		names = append(names, name)
		fmt.Fprintf(&b, " %s = databaseWatchPart %q\n %s -> db_watch_queue %q\n", name, r.Resource.Kind+" events", name, r.Mapping+" "+strings.Join(r.Predicates, ", "))
	}
	fmt.Fprintf(&b, "}\nviews { view database_watches { title %q\n include %s\n } }\n", "Database controller: watches extracted from Go setup", strings.Join(names, ", "))
	return b.String()
}

// Evaluate an extracted field-path mapping over the declared object identity.
// Identity fields are fixed in this bounded domain; arbitrary map functions fail extraction.
func mappingTarget(rule watchgen.Rule) (client.ObjectKey, error) {
	var object any = &apiv1.Database{ObjectMeta: metav1.ObjectMeta{Namespace: databaseKey.Namespace, Name: databaseKey.Name}}
	if rule.Resource.Kind == "DatabaseInstance" {
		object = &apiv1.DatabaseInstance{ObjectMeta: metav1.ObjectMeta{Namespace: databaseKey.Namespace, Name: "instance-for-db-1"}, Spec: apiv1.DatabaseInstanceSpec{DatabaseName: databaseKey.Name}}
	}
	if len(rule.NamePath) != 1 || rule.NamePath[0] != "Name" {
		if rule.Resource.Kind != "DatabaseInstance" || strings.Join(rule.NamePath, ".") != "Spec.DatabaseName" {
			return client.ObjectKey{}, fmt.Errorf("mapping depends on a field outside the fixed identity domain: %v", rule.NamePath)
		}
	}
	value := reflect.ValueOf(object)
	for _, field := range rule.NamePath {
		if value.Kind() == reflect.Pointer {
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct {
			return client.ObjectKey{}, fmt.Errorf("invalid field mapping")
		}
		value = value.FieldByName(field)
		if !value.IsValid() {
			return client.ObjectKey{}, fmt.Errorf("unknown mapping field")
		}
	}
	if value.Kind() != reflect.String {
		return client.ObjectKey{}, fmt.Errorf("request name must be a string")
	}
	return client.ObjectKey{Namespace: databaseKey.Namespace, Name: value.String()}, nil
}

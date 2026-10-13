package assignment

import (
	"fmt"
	apiv1 "generative-architecture/examples/machinevendor/api/v1alpha1"
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

var infraKey = client.ObjectKey{Namespace: "demo", Name: "infra-1"}

func extractWatches(source []byte) (watchgen.Result, error) {
	resources := map[string]effect.Resource{}
	for _, kind := range []string{"InfraMachine", "MachineClaim"} {
		resources["generative-architecture/examples/machinevendor/api/v1alpha1."+kind] = effect.Resource{Group: "demo.architecture.local", Version: "v1alpha1", Kind: kind}
	}
	watches, err := watchgen.Extract(source, "MAC", "SetupWithManager", resources)
	if err != nil {
		return watches, err
	}
	for _, r := range watches.Rules {
		if r.Mapping == "self" {
			if r.Resource.Kind != "InfraMachine" {
				return watches, fmt.Errorf("unsupported primary resource")
			}
			continue
		}
		if r.Mapping != "ClaimToInfraMachine" || r.Resource.Kind != "MachineClaim" {
			return watches, fmt.Errorf("unsummarized mapping %s", r.Mapping)
		}

	}
	return watches, nil
}

// addWatchModel composes the extracted registrations with a reviewed one-key FIFO
// summary. Events may be delivered while processing; queued then means a dirty key.
func addWatchModel(spec model.Spec, w watchgen.Result) (model.Spec, error) {
	for _, name := range []string{"queued", "wrongKey", "claimUpdatePending"} {
		spec.State[name] = model.Variable{Type: "bool", Initial: "false"}
	}
	for _, name := range []string{"infraEvent", "claimEvent"} {
		spec.State[name] = model.Variable{Type: "string", Initial: `""`}
	}
	for i := range spec.Actions {
		a := &spec.Actions[i]
		switch a.Name {
		case "discoverCompatible", "discoverUnsupported":
			a.Updates["infraEvent"] = `"create"`
		case "fulfillClaim":
			a.Updates["claimEvent"] = `claimEvent == "create" ? "create" : "update"`
			a.Updates["claimUpdatePending"] = `claimEvent == "create" || claimUpdatePending`
		case "reconcile":
			a.Guard = "finished && queued && invocations < 4"
			a.Updates["queued"] = "false"
		}
		if a.Effect == "createClaim" {
			a.Updates["claimEvent"] = `"create"`
		}
		if a.Effect == "publish" {
			a.Updates["infraEvent"] = `"update"`
		}
	}
	for _, r := range []struct{ event, kind, action string }{{"infraEvent", "InfraMachine", "deliverInfra"}, {"claimEvent", "MachineClaim", "deliverClaim"}} {
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
			if target == infraKey {
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
		if r.kind == "MachineClaim" {
			updates[r.event] = `claimEvent == "create" && claimUpdatePending ? "update" : ""`
			updates["claimUpdatePending"] = "false"
		}
		spec.Actions = append(spec.Actions, model.Action{Name: r.action, Guard: expr.Expression(r.event + ` != ""`), Updates: updates})
	}
	// A delayed event can refer to an object already gone by reconciliation time.
	spec.Actions = append(spec.Actions, model.Action{Name: "removeInfraBeforeRead", Guard: "infraExists && !claimExists && invocations == 0", Updates: map[string]expr.Expression{"infraExists": "false", "contractSatisfied": "false", "infraEvent": `"delete"`}})
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
	pending := a.infraEvent
	if kind == "MachineClaim" {
		pending = a.claimEvent
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
		if kind == "InfraMachine" {
			h = &handler.EnqueueRequestForObject{}
			obj = &apiv1.InfraMachine{ObjectMeta: metav1.ObjectMeta{Namespace: infraKey.Namespace, Name: infraKey.Name}, Status: apiv1.InfraMachineStatus{MachineID: a.statusMachineId}}
		} else {
			h = handler.EnqueueRequestsFromMapFunc(ClaimToInfraMachine)
			obj = &apiv1.MachineClaim{ObjectMeta: metav1.ObjectMeta{Namespace: infraKey.Namespace, Name: "claim-for-infra-1"}, Spec: apiv1.MachineClaimSpec{InfraMachineName: infraKey.Name}, Status: apiv1.Assignment{MachineID: a.claimMachineId}}
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
	if kind == "MachineClaim" {
		if a.claimEvent == "create" && a.claimUpdatePending {
			a.claimEvent = "update"
		} else {
			a.claimEvent = ""
		}
		a.claimUpdatePending = false
	} else {
		a.infraEvent = ""
	}
	return a.queue.err
}
func (e Example) WatchLikeC4() string {
	var b strings.Builder
	fmt.Fprintln(&b, "specification { element assignmentWatchPart }\nmodel {")
	fmt.Fprintln(&b, ` mac_watch_queue = assignmentWatchPart "MAC workqueue"
 mac_watch_reconcile = assignmentWatchPart "MAC reconcile"
 mac_watch_queue -> mac_watch_reconcile "dequeue InfraMachine key"`)
	names := []string{"mac_watch_queue", "mac_watch_reconcile"}
	for i, r := range e.Watches.Rules {
		name := fmt.Sprintf("mac_watch_source%d", i)
		names = append(names, name)
		fmt.Fprintf(&b, " %s = assignmentWatchPart %q\n %s -> mac_watch_queue %q\n", name, r.Resource.Kind+" events", name, r.Mapping+" "+strings.Join(r.Predicates, ", "))
	}
	fmt.Fprintf(&b, "}\nviews { view mac_assignment_watches { title %q\n include %s\n } }\n", "MAC: watches extracted from Go setup", strings.Join(names, ", "))
	return b.String()
}

// Evaluate an extracted field-path mapping over the declared object identity.
// Identity fields are fixed in this bounded domain; arbitrary map functions fail extraction.
func mappingTarget(rule watchgen.Rule) (client.ObjectKey, error) {
	var object any = &apiv1.InfraMachine{ObjectMeta: metav1.ObjectMeta{Namespace: infraKey.Namespace, Name: infraKey.Name}}
	if rule.Resource.Kind == "MachineClaim" {
		object = &apiv1.MachineClaim{ObjectMeta: metav1.ObjectMeta{Namespace: infraKey.Namespace, Name: "claim-for-infra-1"}, Spec: apiv1.MachineClaimSpec{InfraMachineName: infraKey.Name}}
	}
	if len(rule.NamePath) != 1 || rule.NamePath[0] != "Name" {
		if rule.Resource.Kind != "MachineClaim" || strings.Join(rule.NamePath, ".") != "Spec.InfraMachineName" {
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
	return client.ObjectKey{Namespace: infraKey.Namespace, Name: value.String()}, nil
}

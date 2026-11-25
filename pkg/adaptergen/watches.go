package adaptergen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/watchmodel"
)

// WatchKind is one watched kind of a controller: the resource, the fixture
// object the replay delivers events about and the mapping fields of that
// object. The rules that watch the kind, with their mappings and
// predicates, reach the generated delivery at run time, so a changed
// wiring is delivered as extracted without regenerating.
type WatchKind struct {
	Resource ir.GVK
	// Namespace and Name are the fixture object's key.
	Namespace, Name string
	// Fields are the object's mapping fields: the Go field path the watch
	// model names the variable by, the JSON path the object stores it at
	// and the initial value, a Go string literal.
	Fields []WatchField
}

// WatchField is one mapping field of a watched object.
type WatchField struct {
	GoPath, JSONPath []string
	Initial          string
}

// WatchDelivery describes the native watch delivery of one controller: its
// watched kinds and the reconcile key whose queued flag the watch model
// observes.
type WatchDelivery struct {
	Kinds []WatchKind
	Key   ir.ObjectRef
}

type watchFieldData struct {
	Var, SeenVar string
	JSONPath     []string
	Initial      string
}

type watchKindData struct {
	Kind, Deliver                    string
	GVK                              ir.GVK
	Namespace, Name                  string
	EventVar, PendingVar             string
	GenerationVar, GenerationSeenVar string
	VersionVar, VersionSeenVar       string
	Fields                           []watchFieldData
}

type watchData struct {
	Package   string
	Kinds     []watchKindData
	QueuedVar string
	WrongKey  string
}

// GenerateWatchDelivery emits a file of package pkg defining the type
// Watches, the native watch delivery of the controller d describes:
//
//	type WatchRule struct { Mapping string; Unstructured bool; Predicates []string }
//	func NewWatches() *Watches
//	func WatchKindOf(event string) (string, bool)
//	func (w *Watches) Record(kind, event string, generation bool) bool
//	func (w *Watches) Deliver(ctx context.Context, kind string, rules []WatchRule, queue WatchQueue, handlers map[string]handler.EventHandler, typed map[string]func() client.Object) error
//	func (w *Watches) Observe(into map[string]any, queue WatchQueue)
//
// Watches keeps the informer's view of each watched object: its undelivered
// event and its generation, resource version and mapping fields now and as
// last delivered. Record notes a change the environment or the controller
// makes, as the watch model's Emit does; WatchKindOf maps a deliver action
// to its kind. Deliver passes the object's event through every rule the
// caller extracted on the kind: the native predicates the rule names, then
// the native handler, the one for the object's own key for mapping self or
// the one the caller supplies under the rule's mapping name, into the
// queue. A typed rule delivers the object the caller's constructor for the
// kind returns, filled from the same fields; an unstructured rule delivers
// an unstructured object. Observe writes the watch state under the watch
// model's names. The file observes the implementation only and never
// consults expected model state.
func GenerateWatchDelivery(pkg string, d WatchDelivery) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	if len(d.Kinds) == 0 {
		return nil, fmt.Errorf("watches: at least one watched kind is required")
	}
	if d.Key.Name == "" || d.Key.GVK.Kind == "" {
		return nil, fmt.Errorf("watches: the reconcile key needs a kind and a name")
	}
	out := watchData{Package: pkg, QueuedVar: watchmodel.QueuedVar(d.Key), WrongKey: watchmodel.WrongKeyVar}
	seen := map[string]bool{}
	for _, k := range d.Kinds {
		if k.Resource.Kind == "" || k.Resource.Version == "" {
			return nil, fmt.Errorf("watches: a kind needs a version and a kind, got %+v", k.Resource)
		}
		if seen[k.Resource.Kind] {
			return nil, fmt.Errorf("watches: kind %s listed twice", k.Resource.Kind)
		}
		seen[k.Resource.Kind] = true
		if k.Name == "" {
			return nil, fmt.Errorf("watches: the %s object needs a name", k.Resource.Kind)
		}
		kd := watchKindData{
			Kind: k.Resource.Kind, Deliver: watchmodel.DeliverAction(k.Resource), GVK: k.Resource, Namespace: k.Namespace, Name: k.Name,
			EventVar: watchmodel.EventVar(k.Resource), PendingVar: watchmodel.PendingVar(k.Resource),
			GenerationVar: watchmodel.GenerationVar(k.Resource), GenerationSeenVar: watchmodel.SeenVar(watchmodel.GenerationVar(k.Resource)),
			VersionVar: watchmodel.ResourceVersionVar(k.Resource), VersionSeenVar: watchmodel.SeenVar(watchmodel.ResourceVersionVar(k.Resource)),
		}
		for _, f := range k.Fields {
			if len(f.GoPath) == 0 || len(f.JSONPath) == 0 {
				return nil, fmt.Errorf("watches: a field of %s needs a Go path and a JSON path", k.Resource.Kind)
			}
			if _, err := strconv.Unquote(f.Initial); err != nil {
				return nil, fmt.Errorf("watches: field %s of %s: initial %q is not a string literal", strings.Join(f.GoPath, "."), k.Resource.Kind, f.Initial)
			}
			v := watchmodel.FieldVar(k.Resource, f.GoPath)
			kd.Fields = append(kd.Fields, watchFieldData{Var: v, SeenVar: watchmodel.SeenVar(v), JSONPath: f.JSONPath, Initial: f.Initial})
		}
		out.Kinds = append(out.Kinds, kd)
	}
	sort.Slice(out.Kinds, func(i, j int) bool { return out.Kinds[i].Kind < out.Kinds[j].Kind })
	return render("watches", watchTemplate, out)
}

const watchTemplate = Header + `

package {{.Package}}

import (
	"context"
	"fmt"
	"strconv"

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

// WatchQueue is the native work queue the delivery feeds: Deliver hands the
// native handler the queue, Queued reports whether a delivery queued a key
// and Err the first delivery outside the declared keys.
type WatchQueue interface {
	Deliver(func(workqueue.TypedRateLimitingInterface[reconcile.Request]))
	Queued() bool
	Err() error
}

// WatchRule is one extracted watch on a kind, as the caller loads it:
// Mapping is "self" for the object's own key, else the name of the handler
// the caller supplies; Unstructured says the rule delivers unstructured
// objects; Predicates names the native predicates the rule registers.
type WatchRule struct {
	Mapping      string
	Unstructured bool
	Predicates   []string
}

// watchedObject is the informer's view of one object: its undelivered
// event, and its generation, resource version and mapping fields now and
// when last delivered. The last delivered copy is the ObjectOld of the next
// update.
type watchedObject struct {
	event                               string
	pending                             bool
	generation, resourceVersion         int64
	generationSeen, resourceVersionSeen int64
	fields, fieldsSeen                  map[string]string
}

// Watches is the watch state of the controller's watched objects.
type Watches struct {
	objects map[string]*watchedObject
}

// NewWatches returns the informer view of every watched object before any
// event, with each mapping field at its initial value.
func NewWatches() *Watches {
	return &Watches{objects: map[string]*watchedObject{
{{- range .Kinds}}
		{{q .Kind}}: {fields: map[string]string{ {{- range .Fields}}{{q .Var}}: {{.Initial}}, {{end -}} }, fieldsSeen: map[string]string{ {{- range .Fields}}{{q .Var}}: {{.Initial}}, {{end -}} }},
{{- end}}
	}}
}

// WatchKindOf maps a deliver action of the watch model to its kind.
func WatchKindOf(event string) (string, bool) {
	switch event {
{{- range .Kinds}}
	case {{q .Deliver}}:
		return {{q .Kind}}, true
{{- end}}
	}
	return "", false
}

// Record notes a change of the object of kind, as the API server moves its
// resource version and, on a create or a spec change, its generation. An
// update behind an undelivered create waits; other events coalesce. It
// returns false for a kind nobody watches.
func (w *Watches) Record(kind, ev string, generation bool) bool {
	o, ok := w.objects[kind]
	if !ok {
		return false
	}
	o.resourceVersion++
	switch ev {
	case "create":
		o.event, o.pending = ev, false
		o.generation++
		o.generationSeen, o.resourceVersionSeen = o.generation, o.resourceVersion
	case "delete":
		o.event, o.pending = ev, false
	case "update":
		o.pending = o.pending || o.event == "create"
		if o.event == "" {
			o.event = ev
		}
		if generation {
			o.generation++
		}
	}
	return true
}

// Observe writes the watch state under the watch model's names.
func (w *Watches) Observe(into map[string]any, queue WatchQueue) {
	into[{{q .QueuedVar}}] = queue.Queued()
	into[{{q .WrongKey}}] = queue.Err() != nil
{{- range .Kinds}}
	{
		o := w.objects[{{q .Kind}}]
		into[{{q .EventVar}}] = o.event
		into[{{q .PendingVar}}] = o.pending
		into[{{q .GenerationVar}}] = o.generation
		into[{{q .GenerationSeenVar}}] = o.generationSeen
		into[{{q .VersionVar}}] = o.resourceVersion
		into[{{q .VersionSeenVar}}] = o.resourceVersionSeen
{{- range .Fields}}
		into[{{q .Var}}] = o.fields[{{q .Var}}]
		into[{{q .SeenVar}}] = o.fieldsSeen[{{q .Var}}]
{{- end}}
	}
{{- end}}
}

// Deliver hands the event of the object of kind to every rule on the kind,
// through the native predicates and handlers, into queue. handlers supplies
// the native handler of each mapping function by name and typed the empty
// typed object of each kind a typed rule delivers. A delivered create keeps
// the copy taken at creation, so an update behind it compares against the
// created object.
func (w *Watches) Deliver(ctx context.Context, kind string, rules []WatchRule, queue WatchQueue, handlers map[string]handler.EventHandler, typed map[string]func() client.Object) error {
	o, ok := w.objects[kind]
	if !ok {
		return fmt.Errorf("no watch on kind %s", kind)
	}
	if o.event == "" {
		return fmt.Errorf("no %s event to deliver", kind)
	}
	for _, rule := range rules {
		obj, old, err := w.objectsOf(kind, o, rule.Unstructured, typed[kind])
		if err != nil {
			return err
		}
		var h handler.EventHandler = &handler.EnqueueRequestForObject{}
		if rule.Mapping != "self" {
			if h, ok = handlers[rule.Mapping]; !ok {
				return fmt.Errorf("no handler supplied for mapping %s", rule.Mapping)
			}
		}
		allow := true
		for _, name := range rule.Predicates {
			p, err := watchPredicate(name)
			if err != nil {
				return err
			}
			allow = allow && watchAllow(p, o.event, obj, old)
		}
		if !allow {
			continue
		}
		if err := watchDeliver(ctx, queue, h, o.event, obj, old); err != nil {
			return err
		}
	}
	if o.event != "create" {
		o.generationSeen, o.resourceVersionSeen = o.generation, o.resourceVersion
		for k, v := range o.fields {
			o.fieldsSeen[k] = v
		}
	}
	o.event = ""
	if o.pending {
		o.event = "update"
	}
	o.pending = false
	return queue.Err()
}

// watchPredicate resolves a predicate name of the extracted rules to the
// native predicate.
func watchPredicate(name string) (predicate.Predicate, error) {
	switch name {
	case "generationChanged":
		return predicate.GenerationChangedPredicate{}, nil
	case "resourceVersionChanged":
		return predicate.ResourceVersionChangedPredicate{}, nil
	}
	return nil, fmt.Errorf("unsupported predicate %s", name)
}

// watchAllow applies a native predicate to the event.
func watchAllow(p predicate.Predicate, ev string, obj, old client.Object) bool {
	switch ev {
	case "create":
		return p.Create(event.CreateEvent{Object: obj})
	case "update":
		return p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: obj})
	case "delete":
		return p.Delete(event.DeleteEvent{Object: obj})
	}
	return false
}

// watchDeliver hands the event to the native handler through the queue.
func watchDeliver(ctx context.Context, queue WatchQueue, h handler.EventHandler, ev string, obj, old client.Object) error {
	switch ev {
	case "create":
		queue.Deliver(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) { h.Create(ctx, event.CreateEvent{Object: obj}, q) })
	case "update":
		queue.Deliver(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			h.Update(ctx, event.UpdateEvent{ObjectOld: old, ObjectNew: obj}, q)
		})
	case "delete":
		queue.Deliver(func(q workqueue.TypedRateLimitingInterface[reconcile.Request]) { h.Delete(ctx, event.DeleteEvent{Object: obj}, q) })
	default:
		return fmt.Errorf("unsupported event %s", ev)
	}
	return nil
}

// objectsOf builds the object an event on the kind carries and the last
// delivered copy, unstructured or typed as the rule watches them.
func (w *Watches) objectsOf(kind string, o *watchedObject, asUnstructured bool, newTyped func() client.Object) (client.Object, client.Object, error) {
	build := func(generation, resourceVersion int64, fields map[string]string) (client.Object, error) {
		u := &unstructured.Unstructured{Object: map[string]any{}}
		switch kind {
{{- range .Kinds}}
		case {{q .Kind}}:
			u.SetGroupVersionKind(schema.GroupVersionKind{Group: {{q .GVK.Group}}, Version: {{q .GVK.Version}}, Kind: {{q .GVK.Kind}}})
			u.SetNamespace({{q .Namespace}})
			u.SetName({{q .Name}})
{{- range .Fields}}
			if err := unstructured.SetNestedField(u.Object, fields[{{q .Var}}], {{path .JSONPath}}); err != nil {
				return nil, err
			}
{{- end}}
{{- end}}
		default:
			return nil, fmt.Errorf("no watch on kind %s", kind)
		}
		u.SetGeneration(generation)
		u.SetResourceVersion(strconv.FormatInt(resourceVersion, 10))
		if asUnstructured {
			return u, nil
		}
		if newTyped == nil {
			return nil, fmt.Errorf("a typed rule on %s needs the kind's object constructor", kind)
		}
		obj := newTyped()
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj); err != nil {
			return nil, err
		}
		return obj, nil
	}
	obj, err := build(o.generation, o.resourceVersion, o.fields)
	if err != nil {
		return nil, nil, err
	}
	old, err := build(o.generationSeen, o.resourceVersionSeen, o.fieldsSeen)
	return obj, old, err
}
`

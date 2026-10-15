package adaptergen

import (
	"fmt"
	"strconv"
	"strings"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/ir"
)

// CachedVariable names the state a variable projects to when its object is
// read through the cache reader rather than the API.
func CachedVariable(name string) string { return "cached_" + name }

// FromSpec derives reader projections from an abstraction spec: for every
// object an existence flag plus one projection per declared field, named as
// the abstraction derives its state variables from the object's ID and read
// through the API reader. Every object whose ID is listed in cached is also
// read through the cache reader, under the CachedVariable names, so the
// projection shows what a cached read would return. A cached ID must name
// exactly one object of the spec.
func FromSpec(spec abstraction.Spec, cached ...string) ([]Projection, error) {
	viaCache := map[string]bool{}
	for _, id := range cached {
		if viaCache[id] {
			return nil, fmt.Errorf("cached object %q listed twice", id)
		}
		viaCache[id] = true
	}
	found := map[string]int{}
	var out []Projection
	for _, r := range spec.Resources {
		for _, o := range r.Objects {
			readers := []string{ReaderAPI}
			if viaCache[o.ID] {
				found[o.ID]++
				readers = append(readers, ReaderCache)
			}
			for _, reader := range readers {
				name := func(v string) string {
					if reader == ReaderCache {
						return CachedVariable(v)
					}
					return v
				}
				key := ir.ObjectRef{GVK: r.GVK, Namespace: o.Namespace, Name: o.Name}
				out = append(out, Projection{State: name(abstraction.ExistsVariable(r.GVK.Kind, o.ID)), Resource: r.GVK, Key: key, Type: abstraction.TypeBool, Reader: reader})
				for _, f := range r.Fields {
					out = append(out, Projection{State: name(abstraction.VariableName(r.GVK.Kind, o.ID, f.Path)), Resource: r.GVK, Key: key, JSONPath: append([]string(nil), f.Path...), Type: f.Type, Reader: reader})
				}
			}
		}
	}
	for _, id := range cached {
		switch found[id] {
		case 0:
			return nil, fmt.Errorf("cached object %q is not an object of the abstraction", id)
		case 1:
		default:
			return nil, fmt.Errorf("cached object %q names objects of %d resources", id, found[id])
		}
	}
	return out, nil
}

// GenerateReaderProjection emits a file of package pkg defining
//
//	func Project(ctx context.Context, api client.Reader[, cache client.Reader]) (map[string]any, error)
//
// over controller-runtime readers: every projected object is read as an
// unstructured object of its kind through the reader its projections name,
// each scalar path is read with unstructured.Nested* and an absent object
// projects as not existing. The cache parameter is present only when some
// projection reads through it. The function takes only API readers and a
// context: it never reads expected model state.
func GenerateReaderProjection(pkg string, ps []Projection) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	objects, err := groupProjections(ps)
	if err != nil {
		return nil, err
	}
	cached := false
	for _, o := range objects {
		cached = cached || o.Reader == ReaderCache
	}
	return render("reader projection", readerProjectionTemplate, struct {
		Header  string
		Package string
		Cached  bool
		Objects []projectedObject
	}{Header, pkg, cached, objects})
}

const readerProjectionTemplate = `{{.Header}}

package {{.Package}}

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Project reads the projected state of every declared object through api
{{- if .Cached}}, and
// of the objects a cached read observes also through cache{{end}}. It observes
// implementation state only and never consults expected model state. An
// absent object projects its existence flags as false and its fields as zero
// values.
func Project(ctx context.Context, api client.Reader{{if .Cached}}, cache client.Reader{{end}}) (map[string]any, error) {
	state := map[string]any{}
{{- range .Objects}}
	// {{.Key}} through {{.Reader}}
	{
		obj, found, err := projectRead(ctx, {{.Reader}}, {{gvk .Key.GVK}}, {{q .Key.Namespace}}, {{q .Key.Name}})
		if err != nil {
			return nil, err
		}
{{- range .Exists}}
		state[{{q .}}] = found
{{- end}}
{{- range .Fields}}
		state[{{q .State}}] = {{.Zero}}
{{- end}}
{{- if .Fields}}
		if found {
{{- range .Fields}}
			if v, ok, err := unstructured.{{.Read}}(obj.Object, {{path .Path}}); err != nil {
				return nil, fmt.Errorf("%s: %w", {{q .State}}, err)
			} else if ok {
				state[{{q .State}}] = v
			}
{{- end}}
		}
{{- end}}
	}
{{- end}}
	return state, nil
}

// projectRead gets one object as unstructured content of its kind. A missing
// object is reported as not found rather than as an error.
func projectRead(ctx context.Context, reader client.Reader, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, bool, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, obj)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s %s/%s: %w", gvk.Kind, namespace, name, err)
	}
	return obj, true, nil
}
`

// DeliverCacheEvent is the environment event that delivers the API state of
// the cached objects to the cache reader.
const DeliverCacheEvent = "deliverCache"

// ChangeEvent is the environment event that changes the object objectID.
func ChangeEvent(objectID string) string {
	if objectID == "" {
		return "change"
	}
	return "change" + strings.ToUpper(objectID[:1]) + objectID[1:]
}

type cycledField struct {
	Path   []string
	Values []string // Go literals, in domain order
}

type changedObject struct {
	Name   string
	Key    ir.ObjectRef
	Fields []cycledField
}

type deliveredObject struct {
	Key   ir.ObjectRef
	Paths [][]string
}

// domainLiterals renders the value domain of a field as Go literals of the
// type unstructured content holds. The zero value of a string or int field
// stands for unset and is left out, so a change always sets a value; a bool
// field cycles through its whole domain.
func domainLiterals(typ string, domain []string) ([]string, error) {
	var out []string
	for _, v := range domain {
		switch typ {
		case abstraction.TypeString:
			s, err := strconv.Unquote(v)
			if err != nil {
				return nil, fmt.Errorf("value %s is not a string literal", v)
			}
			if s != "" {
				out = append(out, strconv.Quote(s))
			}
		case abstraction.TypeInt:
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("value %s is not an int literal", v)
			}
			if n != 0 {
				out = append(out, fmt.Sprintf("int64(%d)", n))
			}
		case abstraction.TypeBool:
			if v != "true" && v != "false" {
				return nil, fmt.Errorf("value %s is not a bool literal", v)
			}
			out = append(out, v)
		default:
			return nil, fmt.Errorf("unknown type %q", typ)
		}
	}
	return out, nil
}

// lookupObject finds the one object of the spec with the given ID.
func lookupObject(spec abstraction.Spec, id string) (abstraction.Resource, ir.ObjectRef, error) {
	var found []abstraction.Resource
	var key ir.ObjectRef
	for _, r := range spec.Resources {
		for _, o := range r.Objects {
			if o.ID == id {
				found = append(found, r)
				key = ir.ObjectRef{GVK: r.GVK, Namespace: o.Namespace, Name: o.Name}
			}
		}
	}
	switch len(found) {
	case 0:
		return abstraction.Resource{}, key, fmt.Errorf("object %q is not an object of the abstraction", id)
	case 1:
		k, err := objectKey(found[0].GVK, key)
		return found[0], k, err
	}
	return abstraction.Resource{}, key, fmt.Errorf("object %q names objects of %d resources", id, len(found))
}

// GenerateReaderEnvironment emits a file of package pkg defining
//
//	func ApplyEnvironment(ctx context.Context, api client.Client[, cache client.Client], event string) (bool, error)
//
// whose events are derived from the abstraction spec:
//
//   - ChangeEvent(id) for every object ID in changed: the environment, not
//     the controller, moves every declared field of that object with a value
//     domain to the next value of its domain (see domainLiterals; a value
//     outside the domain moves to the first) and updates the object through
//     api;
//   - DeliverCacheEvent when cached is not empty: every declared field of
//     the cached objects is copied from the API state to the cache, which
//     is how a cache-backed read catches up with the API.
//
// The cache parameter is present only when cached is not empty. The result
// reports whether event named an environment action, so callers can route
// other events elsewhere. The function takes only API clients.
func GenerateReaderEnvironment(pkg string, spec abstraction.Spec, changed, cached []string) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	if len(changed) == 0 && len(cached) == 0 {
		return nil, fmt.Errorf("at least one changed or cached object is required")
	}
	seen := map[string]bool{}
	var changes []changedObject
	for _, id := range changed {
		if seen["change/"+id] {
			return nil, fmt.Errorf("changed object %q listed twice", id)
		}
		seen["change/"+id] = true
		r, key, err := lookupObject(spec, id)
		if err != nil {
			return nil, fmt.Errorf("changed %w", err)
		}
		c := changedObject{Name: ChangeEvent(id), Key: key}
		for _, f := range r.Fields {
			domain, ok := r.ValueDomains[abstraction.PathKey(f.Path)]
			if !ok {
				continue
			}
			values, err := domainLiterals(f.Type, domain)
			if err != nil {
				return nil, fmt.Errorf("changed object %s, field %s: %w", id, abstraction.PathKey(f.Path), err)
			}
			if len(values) == 0 {
				return nil, fmt.Errorf("changed object %s, field %s: the domain has no value to change to", id, abstraction.PathKey(f.Path))
			}
			c.Fields = append(c.Fields, cycledField{Path: append([]string(nil), f.Path...), Values: values})
		}
		if len(c.Fields) == 0 {
			return nil, fmt.Errorf("changed object %s has no declared field with a value domain", id)
		}
		changes = append(changes, c)
	}
	var deliveries []deliveredObject
	for _, id := range cached {
		if seen["cache/"+id] {
			return nil, fmt.Errorf("cached object %q listed twice", id)
		}
		seen["cache/"+id] = true
		r, key, err := lookupObject(spec, id)
		if err != nil {
			return nil, fmt.Errorf("cached %w", err)
		}
		if len(r.Fields) == 0 {
			return nil, fmt.Errorf("cached object %s has no declared field to deliver", id)
		}
		d := deliveredObject{Key: key}
		for _, f := range r.Fields {
			d.Paths = append(d.Paths, append([]string(nil), f.Path...))
		}
		deliveries = append(deliveries, d)
	}
	return render("reader environment", readerEnvironmentTemplate, struct {
		Header     string
		Package    string
		Deliver    string
		Changes    []changedObject
		Deliveries []deliveredObject
	}{Header, pkg, DeliverCacheEvent, changes, deliveries})
}

const readerEnvironmentTemplate = `{{.Header}}

package {{.Package}}

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ApplyEnvironment performs the environment action named event: a change
// the environment, not the controller, makes between replay steps. It
// returns false when event names no environment action.
func ApplyEnvironment(ctx context.Context, api client.Client{{if .Deliveries}}, cache client.Client{{end}}, event string) (bool, error) {
	switch event {
{{- range .Changes}}
	case {{q .Name}}:
		// change {{.Key}} to the next value of each field's domain
		obj, err := environmentRead(ctx, api, {{gvk .Key.GVK}}, {{q .Key.Namespace}}, {{q .Key.Name}})
		if err != nil {
			return true, err
		}
{{- range .Fields}}
		if err := environmentCycle(obj, []any{ {{- range $i, $v := .Values}}{{if $i}}, {{end}}{{$v}}{{end -}} }, {{path .Path}}); err != nil {
			return true, err
		}
{{- end}}
		return true, api.Update(ctx, obj)
{{- end}}
{{- if .Deliveries}}
	case {{q .Deliver}}:
{{- range .Deliveries}}
		// deliver {{.Key}} to the cache
		{
			live, err := environmentRead(ctx, api, {{gvk .Key.GVK}}, {{q .Key.Namespace}}, {{q .Key.Name}})
			if err != nil {
				return true, err
			}
			cached, err := environmentRead(ctx, cache, {{gvk .Key.GVK}}, {{q .Key.Namespace}}, {{q .Key.Name}})
			if err != nil {
				return true, err
			}
{{- range .Paths}}
			if err := environmentCopy(live, cached, {{path .}}); err != nil {
				return true, err
			}
{{- end}}
			if err := cache.Update(ctx, cached); err != nil {
				return true, err
			}
		}
{{- end}}
		return true, nil
{{- end}}
	}
	return false, nil
}

// environmentRead gets one object as unstructured content of its kind.
func environmentRead(ctx context.Context, reader client.Reader, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, obj); err != nil {
		return nil, fmt.Errorf("%s %s/%s: %w", gvk.Kind, namespace, name, err)
	}
	return obj, nil
}

// environmentCycle sets the field at path to the value after its current one
// in values, or to the first value when the current one is not among them.
func environmentCycle(obj *unstructured.Unstructured, values []any, path ...string) error {
	current, _, err := unstructured.NestedFieldNoCopy(obj.Object, path...)
	if err != nil {
		return err
	}
	next := values[0]
	for i, v := range values {
		if v == current {
			next = values[(i+1)%len(values)]
			break
		}
	}
	return unstructured.SetNestedField(obj.Object, next, path...)
}

// environmentCopy copies the field at path from one object to another,
// removing it from the target when the source does not set it.
func environmentCopy(from, to *unstructured.Unstructured, path ...string) error {
	v, found, err := unstructured.NestedFieldCopy(from.Object, path...)
	if err != nil {
		return err
	}
	if !found {
		unstructured.RemoveNestedField(to.Object, path...)
		return nil
	}
	return unstructured.SetNestedField(to.Object, v, path...)
}
`

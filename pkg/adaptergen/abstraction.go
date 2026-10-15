package adaptergen

import (
	"fmt"

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

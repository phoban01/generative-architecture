package adaptergen

import (
	"fmt"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/ir"
)

// Projection maps one model state variable to one scalar JSON path of one
// API object. A projection with an empty JSONPath is the object's existence
// flag: its State is set to whether the object was found. Field projections
// of an absent object take the zero value of their Type.
type Projection struct {
	State    string
	Resource ir.GVK
	Key      ir.ObjectRef
	JSONPath []string
	// Type is the model type of the value: string, bool or int (read as an
	// int64 through unstructured).
	Type string
	// Reader names the client.Reader parameter a reader projection reads
	// the object through: ReaderAPI (the default when empty) or ReaderCache.
	// Dynamic-client projections accept only the default.
	Reader string
}

// Readers a generated reader projection takes.
const (
	ReaderAPI   = "api"
	ReaderCache = "cache"
)

func (p Projection) reader() string {
	if p.Reader == "" {
		return ReaderAPI
	}
	return p.Reader
}

// Exists reports whether the projection is an object's existence flag.
func (p Projection) Exists() bool { return len(p.JSONPath) == 0 }

// FromAbstraction derives projections from an abstraction document: for every
// object an existence flag plus one projection per declared field, named as
// the abstraction derives its state variables.
func FromAbstraction(a abstraction.Abstraction) []Projection {
	var out []Projection
	for _, r := range a.Spec.Resources {
		for _, o := range r.Objects {
			key := ir.ObjectRef{GVK: r.GVK, Namespace: o.Namespace, Name: o.Name}
			out = append(out, Projection{State: abstraction.ExistsVariable(r.GVK.Kind, o.ID), Resource: r.GVK, Key: key, Type: abstraction.TypeBool})
			for _, f := range r.Fields {
				out = append(out, Projection{State: abstraction.VariableName(r.GVK.Kind, o.ID, f.Path), Resource: r.GVK, Key: key, JSONPath: append([]string(nil), f.Path...), Type: f.Type})
			}
		}
	}
	return out
}

// projectedObject groups the projections of one object. When no existence
// projection is declared for the object, every field projection records its
// own <State>Exists flag.
type projectedObject struct {
	Reader string
	Key    ir.ObjectRef
	Exists []string
	Fields []projectedField
}

type projectedField struct {
	State string
	Path  []string
	Read  string // unstructured.Nested* accessor
	Zero  string // Go literal of the zero value
}

var accessors = map[string]struct{ read, zero string }{
	abstraction.TypeString: {"NestedString", `""`},
	abstraction.TypeBool:   {"NestedBool", "false"},
	abstraction.TypeInt:    {"NestedInt64", "int64(0)"},
}

func groupProjections(ps []Projection) ([]projectedObject, error) {
	if len(ps) == 0 {
		return nil, fmt.Errorf("at least one projection is required")
	}
	type readKey struct {
		reader string
		key    ir.ObjectRef
	}
	var objects []projectedObject
	index := map[readKey]int{}
	states := map[string]bool{}
	for _, p := range ps {
		if p.State == "" {
			return nil, fmt.Errorf("projection of %s has no state variable", p.Key)
		}
		if states[p.State] {
			return nil, fmt.Errorf("state %s is projected twice", p.State)
		}
		states[p.State] = true
		key, err := objectKey(p.Resource, p.Key)
		if err != nil {
			return nil, fmt.Errorf("projection %s: %w", p.State, err)
		}
		if r := p.reader(); r != ReaderAPI && r != ReaderCache {
			return nil, fmt.Errorf("projection %s: unknown reader %q; expected api or cache", p.State, p.Reader)
		}
		acc, ok := accessors[p.Type]
		if !ok {
			return nil, fmt.Errorf("projection %s: unknown type %q; expected string, bool or int", p.State, p.Type)
		}
		for _, elem := range p.JSONPath {
			if elem == "" {
				return nil, fmt.Errorf("projection %s: JSON path has an empty element", p.State)
			}
		}
		i, ok := index[readKey{p.reader(), key}]
		if !ok {
			i = len(objects)
			index[readKey{p.reader(), key}] = i
			objects = append(objects, projectedObject{Reader: p.reader(), Key: key})
		}
		if p.Exists() {
			if p.Type != abstraction.TypeBool {
				return nil, fmt.Errorf("projection %s: an existence flag must be bool, got %s", p.State, p.Type)
			}
			objects[i].Exists = append(objects[i].Exists, p.State)
			continue
		}
		objects[i].Fields = append(objects[i].Fields, projectedField{State: p.State, Path: append([]string(nil), p.JSONPath...), Read: acc.read, Zero: acc.zero})
	}
	for i := range objects {
		if len(objects[i].Exists) > 0 {
			continue
		}
		for _, f := range objects[i].Fields {
			name := f.State + "Exists"
			if states[name] {
				return nil, fmt.Errorf("existence flag %s collides with a projected state", name)
			}
			states[name] = true
			objects[i].Exists = append(objects[i].Exists, name)
		}
	}
	return objects, nil
}

// GenerateProjection emits a file of package pkg defining
//
//	func Project(ctx context.Context, api dynamic.Interface, mapper meta.RESTMapper) (map[string]any, error)
//
// which gets every projected object through the dynamic client, reads each
// scalar path with unstructured.Nested*, records absent objects as
// not existing and returns the model-typed state map. The function takes
// only API clients: it never reads expected model state.
func GenerateProjection(pkg string, ps []Projection) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	objects, err := groupProjections(ps)
	if err != nil {
		return nil, err
	}
	for _, o := range objects {
		if o.Reader != ReaderAPI {
			return nil, fmt.Errorf("projection of %s: a dynamic-client projection reads through the API only, not %s", o.Key, o.Reader)
		}
	}
	return render("projection", projectionTemplate, struct {
		Header  string
		Package string
		Objects []projectedObject
	}{Header, pkg, objects})
}

const projectionTemplate = `{{.Header}}

package {{.Package}}

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Project reads the projected state of every declared object through api,
// resolving resources with mapper. It observes implementation state only and
// never consults expected model state. An absent object projects its
// existence flags as false and its fields as zero values.
func Project(ctx context.Context, api dynamic.Interface, mapper meta.RESTMapper) (map[string]any, error) {
	state := map[string]any{}
{{- range .Objects}}
	// {{.Key}}
	{
		obj, found, err := projectGet(ctx, api, mapper, {{gvk .Key.GVK}}, {{q .Key.Namespace}}, {{q .Key.Name}})
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

// projectGet fetches one object by kind, namespace and name. A missing object
// is reported as not found rather than as an error.
func projectGet(ctx context.Context, api dynamic.Interface, mapper meta.RESTMapper, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, bool, error) {
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, false, err
	}
	var client dynamic.ResourceInterface = api.Resource(mapping.Resource)
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if namespace == "" {
			return nil, false, fmt.Errorf("%s %s: namespaced resource needs a namespace", gvk, name)
		}
		client = api.Resource(mapping.Resource).Namespace(namespace)
	} else if namespace != "" {
		return nil, false, fmt.Errorf("%s %s/%s: cluster-scoped resource has no namespace", gvk, namespace, name)
	}
	obj, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return obj, true, nil
}
`

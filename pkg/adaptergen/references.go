package adaptergen

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

// referenceCheck is one object whose field must name an object of the
// referenced population.
type referenceCheck struct {
	Key    ir.ObjectRef
	Path   []string
	Target string
	// Field is the referring path joined with dots.
	Field string
	// Names are the names of the declared target objects the referring
	// object can address.
	Names []string
	// Empty allows an absent or empty reference.
	Empty bool
}

// referenceChecks derives one check per object of every resource that
// declares references. A reference names its target by metadata.name; a
// target addressed by another identity is not supported. A namespaced
// referring object can name the target objects of its own namespace and
// cluster-scoped ones. An absent or empty reference is accepted only when
// the referring field declares the empty string in its value domain.
func referenceChecks(spec abstraction.Spec) ([]referenceCheck, error) {
	targets := map[ir.GVK]abstraction.Resource{}
	for _, r := range spec.Resources {
		targets[r.GVK] = r
	}
	var out []referenceCheck
	for _, r := range spec.Resources {
		for _, ref := range r.References {
			path := abstraction.PathKey(ref.Path)
			if len(ref.Path) == 0 {
				return nil, fmt.Errorf("resource %s: reference has no path", r.GVK)
			}
			target, ok := targets[ref.TargetGVK]
			if !ok {
				return nil, fmt.Errorf("resource %s: reference %s targets undeclared resource %s", r.GVK, path, ref.TargetGVK)
			}
			for _, id := range target.Identity {
				if !reflect.DeepEqual(id, []string{"metadata", "name"}) {
					return nil, fmt.Errorf("resource %s: reference %s: target %s is identified by %s; only metadata.name is supported", r.GVK, path, ref.TargetGVK, abstraction.PathKey(id))
				}
			}
			empty := false
			for _, f := range r.Fields {
				if reflect.DeepEqual(f.Path, ref.Path) && f.Equals == "" {
					empty = slices.Contains(r.ValueDomains[f.Key()], `""`)
				}
			}
			for _, o := range r.Objects {
				key, err := objectKey(r.GVK, ir.ObjectRef{Namespace: o.Namespace, Name: o.Name})
				if err != nil {
					return nil, fmt.Errorf("reference %s: %w", path, err)
				}
				c := referenceCheck{Key: key, Path: abstraction.JSONPath(ref.Path), Field: path, Target: ref.TargetGVK.Kind, Empty: empty}
				for _, t := range target.Objects {
					if t.Namespace == "" || t.Namespace == o.Namespace {
						c.Names = append(c.Names, t.Name)
					}
				}
				if len(c.Names) == 0 {
					return nil, fmt.Errorf("reference %s of %s: no declared %s can be named from namespace %q", path, key, ref.TargetGVK.Kind, o.Namespace)
				}
				out = append(out, c)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the abstraction declares no references")
	}
	return out, nil
}

// GenerateReferenceCheck emits a file of package pkg defining
//
//	func CheckReferences(ctx context.Context, api dynamic.Interface, mapper meta.RESTMapper) error
//
// with one check per object of every resource whose abstraction declares
// references. Each check reads the referring object through the dynamic
// client and fails unless the referring field names a declared object of
// the target resource (see referenceChecks). An absent referring object
// passes. Callers run it beside Project, so a projection never reports a
// state whose references point outside the abstraction. The function
// takes only API clients: it never reads expected model state.
func GenerateReferenceCheck(pkg string, spec abstraction.Spec) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	checks, err := referenceChecks(spec)
	if err != nil {
		return nil, err
	}
	return render("references", referencesTemplate, struct {
		Header  string
		Package string
		Checks  []referenceCheck
	}{Header, pkg, checks})
}

const referencesTemplate = `{{.Header}}

package {{.Package}}

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// CheckReferences reads every object that declares a reference through api
// and fails when the reference names no declared object of its target.
// An absent referring object passes. It observes implementation state only
// and never consults expected model state.
func CheckReferences(ctx context.Context, api dynamic.Interface, mapper meta.RESTMapper) error {
{{- range .Checks}}
	// {{.Key}}: {{.Field}} names a {{.Target}}
	if err := referenceCheck(ctx, api, mapper, {{gvk .Key.GVK}}, {{q .Key.Namespace}}, {{q .Key.Name}}, []string{ {{- path .Path -}} }, {{q .Target}}, []string{ {{- path .Names -}} }, {{.Empty}}); err != nil {
		return err
	}
{{- end}}
	return nil
}

// referenceCheck gets one object and checks that the string at path is one
// of names, or empty when empty is allowed.
func referenceCheck(ctx context.Context, api dynamic.Interface, mapper meta.RESTMapper, gvk schema.GroupVersionKind, namespace, name string, path []string, target string, names []string, empty bool) error {
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return err
	}
	var client dynamic.ResourceInterface = api.Resource(mapping.Resource)
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		client = api.Resource(mapping.Resource).Namespace(namespace)
	}
	obj, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	v, _, err := unstructured.NestedString(obj.Object, path...)
	if err != nil {
		return fmt.Errorf("%s %s/%s: %s: %w", gvk.Kind, namespace, name, strings.Join(path, "."), err)
	}
	if v == "" && empty || v != "" && slices.Contains(names, v) {
		return nil
	}
	return fmt.Errorf("%s %s/%s: %s %q names no declared %s", gvk.Kind, namespace, name, strings.Join(path, "."), v, target)
}
`

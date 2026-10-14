package adaptergen

import (
	"fmt"
	"sort"

	"generative-architecture/pkg/ir"
)

// Operations an environment action may perform. Status updates the status
// subresource of an existing object.
const (
	ActionCreate = "create"
	ActionUpdate = "update"
	ActionDelete = "delete"
	ActionStatus = "status"
)

var actionOperations = map[string]bool{ActionCreate: true, ActionUpdate: true, ActionDelete: true, ActionStatus: true}

// EnvAction is one API operation the environment, rather than the controller,
// performs between replay steps: the replay event Name creates, updates,
// deletes or sets the status of the object Key of Resource. Fields map
// dot-joined JSON paths to the literal string values the operation sets;
// path elements therefore cannot contain dots.
type EnvAction struct {
	Name      string
	Operation string
	Resource  ir.GVK
	Key       ir.ObjectRef
	Fields    map[string]string
}

type envField struct {
	Path  []string
	Value string
}

type envCase struct {
	Name      string
	Operation string
	Key       ir.ObjectRef
	Fields    []envField
}

func environmentCases(actions []EnvAction) ([]envCase, error) {
	if len(actions) == 0 {
		return nil, fmt.Errorf("at least one environment action is required")
	}
	names := map[string]bool{}
	cases := make([]envCase, 0, len(actions))
	for _, a := range actions {
		if a.Name == "" {
			return nil, fmt.Errorf("environment action of %s has no name", a.Key)
		}
		if names[a.Name] {
			return nil, fmt.Errorf("environment action %s declared twice", a.Name)
		}
		names[a.Name] = true
		if !actionOperations[a.Operation] {
			return nil, fmt.Errorf("environment action %s: unknown operation %q; expected create, update, delete or status", a.Name, a.Operation)
		}
		key, err := objectKey(a.Resource, a.Key)
		if err != nil {
			return nil, fmt.Errorf("environment action %s: %w", a.Name, err)
		}
		if a.Operation == ActionDelete && len(a.Fields) > 0 {
			return nil, fmt.Errorf("environment action %s: delete sets no fields", a.Name)
		}
		keys := make([]string, 0, len(a.Fields))
		for k := range a.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		c := envCase{Name: a.Name, Operation: a.Operation, Key: key}
		for _, k := range keys {
			path, err := splitPath(k)
			if err != nil {
				return nil, fmt.Errorf("environment action %s: %w", a.Name, err)
			}
			c.Fields = append(c.Fields, envField{Path: path, Value: a.Fields[k]})
		}
		cases = append(cases, c)
	}
	return cases, nil
}

// GenerateEnvironment emits a file of package pkg defining
//
//	func ApplyEnvironment(ctx context.Context, api dynamic.Interface, mapper meta.RESTMapper, event string) (bool, error)
//
// with one switch case per action performing its dynamic-client operation
// with the literal field values. The result reports whether event named an
// environment action, so callers can route other events to process gating.
func GenerateEnvironment(pkg string, actions []EnvAction) ([]byte, error) {
	if err := validPackage(pkg); err != nil {
		return nil, err
	}
	cases, err := environmentCases(actions)
	if err != nil {
		return nil, err
	}
	return render("environment", environmentTemplate, struct {
		Header  string
		Package string
		Cases   []envCase
	}{Header, pkg, cases})
}

const environmentTemplate = `{{.Header}}

package {{.Package}}

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ApplyEnvironment performs the environment action named event: an API
// operation the environment, not the controller, makes between replay steps.
// It returns false when event names no environment action.
func ApplyEnvironment(ctx context.Context, api dynamic.Interface, mapper meta.RESTMapper, event string) (bool, error) {
	switch event {
{{- range .Cases}}
	case {{q .Name}}:
		// {{.Operation}} {{.Key}}
		client, err := environmentClient(api, mapper, {{gvk .Key.GVK}}, {{q .Key.Namespace}})
		if err != nil {
			return true, err
		}
{{- if eq .Operation "create"}}
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind({{gvk .Key.GVK}})
		obj.SetNamespace({{q .Key.Namespace}})
		obj.SetName({{q .Key.Name}})
{{- range .Fields}}
		if err := unstructured.SetNestedField(obj.Object, {{q .Value}}, {{path .Path}}); err != nil {
			return true, err
		}
{{- end}}
		_, err = client.Create(ctx, obj, metav1.CreateOptions{})
		return true, err
{{- else if eq .Operation "delete"}}
		return true, client.Delete(ctx, {{q .Key.Name}}, metav1.DeleteOptions{})
{{- else}}
		obj, err := client.Get(ctx, {{q .Key.Name}}, metav1.GetOptions{})
		if err != nil {
			return true, err
		}
{{- range .Fields}}
		if err := unstructured.SetNestedField(obj.Object, {{q .Value}}, {{path .Path}}); err != nil {
			return true, err
		}
{{- end}}
{{- if eq .Operation "status"}}
		_, err = client.UpdateStatus(ctx, obj, metav1.UpdateOptions{})
{{- else}}
		_, err = client.Update(ctx, obj, metav1.UpdateOptions{})
{{- end}}
		return true, err
{{- end}}
{{- end}}
	}
	return false, nil
}

// environmentClient resolves the resource client for a kind in a namespace,
// checking the namespace against the resource scope.
func environmentClient(api dynamic.Interface, mapper meta.RESTMapper, gvk schema.GroupVersionKind, namespace string) (dynamic.ResourceInterface, error) {
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if namespace == "" {
			return nil, fmt.Errorf("%s: namespaced resource needs a namespace", gvk)
		}
		return api.Resource(mapping.Resource).Namespace(namespace), nil
	}
	if namespace != "" {
		return nil, fmt.Errorf("%s: cluster-scoped resource has no namespace %s", gvk, namespace)
	}
	return api.Resource(mapping.Resource), nil
}
`

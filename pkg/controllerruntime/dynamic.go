package controllerruntime

import (
	"context"
	"fmt"
	"reflect"

	"generative-architecture/pkg/effect"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

// Dynamic observes direct dynamic-client reads and writes without a typed Scheme.
// The mapper must come from discovery or a checked catalog; plural names are never
// guessed. The supported operations are Get, Create, Update, UpdateStatus and
// Delete with zero options, and List with an equality label selector. Other
// methods and options fail closed instead of bypassing instrumentation.
func (o Observer) Dynamic(base dynamic.Interface, mapper meta.RESTMapper) dynamic.Interface {
	return &dynamicProxy{base: base, mapper: mapper, observer: o}
}

type dynamicProxy struct {
	base     dynamic.Interface
	mapper   meta.RESTMapper
	observer Observer
}

func (d *dynamicProxy) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &dynamicResource{parent: d, gvr: gvr}
}

type dynamicResource struct {
	parent    *dynamicProxy
	gvr       schema.GroupVersionResource
	namespace string
}

func (r *dynamicResource) Namespace(ns string) dynamic.ResourceInterface {
	next := *r
	next.namespace = ns
	return &next
}

// resolve checks the endpoint against the mapper and returns its GVK. The
// GVR must map exactly, and the namespace must agree with the resource scope.
func (r *dynamicResource) resolve() (schema.GroupVersionKind, error) {
	if r.parent.base == nil || r.parent.mapper == nil || r.gvr.Version == "" || r.gvr.Resource == "" {
		return schema.GroupVersionKind{}, fmt.Errorf("dynamic effect requires client, mapper and exact GVR")
	}
	gvk, err := r.parent.mapper.KindFor(r.gvr)
	if err != nil {
		return gvk, err
	}
	mapping, err := r.parent.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return gvk, err
	}
	if mapping.Resource != r.gvr || gvk.Group != r.gvr.Group || gvk.Version != r.gvr.Version {
		return gvk, fmt.Errorf("dynamic GVR/GVK mapping is not exact")
	}
	if (mapping.Scope.Name() == meta.RESTScopeNameNamespace && r.namespace == "") || (mapping.Scope.Name() == meta.RESTScopeNameRoot && r.namespace != "") {
		return gvk, fmt.Errorf("dynamic namespace disagrees with resource scope")
	}
	return gvk, nil
}

// observe looks up the effect and runs fn through the hook. fn returns the
// detached snapshot of a successful call, or nil when the call has none. A
// hook that reports success without running fn is an error.
func (r *dynamicResource) observe(ctx context.Context, gvk schema.GroupVersionKind, op, name string, submitted map[string]any, options map[string]string, fn func() (map[string]any, error)) error {
	d, err := r.parent.observer.Registry.LookupFor(r.parent.observer.Actor, effect.Resource{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind}, op, "direct")
	if err != nil {
		return err
	}
	call := effect.Call{Descriptor: d, Key: effect.Key{Namespace: r.namespace, Name: name}, Submitted: submitted, Options: options}
	executed := false
	run := func() effect.Result {
		executed = true
		snapshot, err := fn()
		if err != nil {
			return effect.Result{Err: err}
		}
		return effect.Result{Snapshot: snapshot}
	}
	var result effect.Result
	if r.parent.observer.Hook == nil {
		result = run()
	} else {
		result = r.parent.observer.Hook(ctx, call, run)
	}
	if result.Err != nil {
		return result.Err
	}
	if !executed {
		return fmt.Errorf("dynamic hook returned success without executing request")
	}
	return nil
}

// invoke observes a call on one named object that returns that object. The
// submitted object and the response must match the endpoint's identity.
func (r *dynamicResource) invoke(ctx context.Context, op, name string, obj *unstructured.Unstructured, options map[string]string, execute func(*unstructured.Unstructured) (*unstructured.Unstructured, error)) (*unstructured.Unstructured, error) {
	if name == "" {
		return nil, fmt.Errorf("dynamic effect requires an object name")
	}
	gvk, err := r.resolve()
	if err != nil {
		return nil, err
	}
	var submitted map[string]any
	var input *unstructured.Unstructured
	if obj != nil {
		if obj.GroupVersionKind() != gvk || obj.GetName() != name || obj.GetNamespace() != r.namespace {
			return nil, fmt.Errorf("dynamic object identity disagrees with endpoint")
		}
		input = obj.DeepCopy()
		submitted = input.DeepCopy().Object
	}
	var response *unstructured.Unstructured
	err = r.observe(ctx, gvk, op, name, submitted, options, func() (map[string]any, error) {
		var err error
		response, err = execute(input)
		if err != nil {
			return nil, err
		}
		if response == nil || response.GroupVersionKind() != gvk || response.GetName() != name || response.GetNamespace() != r.namespace {
			return nil, fmt.Errorf("dynamic response identity disagrees with endpoint")
		}
		return response.DeepCopy().Object, nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}
func (r *dynamicResource) base() dynamic.ResourceInterface {
	return r.parent.base.Resource(r.gvr).Namespace(r.namespace)
}
func (r *dynamicResource) Get(ctx context.Context, name string, opts metav1.GetOptions, sub ...string) (*unstructured.Unstructured, error) {
	if opts != (metav1.GetOptions{}) || len(sub) != 0 {
		return nil, unsupported("dynamic.Get options/subresource")
	}
	return r.invoke(ctx, "get", name, nil, nil, func(*unstructured.Unstructured) (*unstructured.Unstructured, error) {
		return r.base().Get(ctx, name, opts)
	})
}
func (r *dynamicResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, sub ...string) (*unstructured.Unstructured, error) {
	if !reflect.DeepEqual(opts, metav1.CreateOptions{}) || len(sub) != 0 || obj == nil {
		return nil, unsupported("dynamic.Create options/subresource/nil object")
	}
	return r.invoke(ctx, "create", obj.GetName(), obj, nil, func(input *unstructured.Unstructured) (*unstructured.Unstructured, error) {
		return r.base().Create(ctx, input, opts)
	})
}
func (r *dynamicResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, sub ...string) (*unstructured.Unstructured, error) {
	if !reflect.DeepEqual(opts, metav1.UpdateOptions{}) || len(sub) != 0 || obj == nil {
		return nil, unsupported("dynamic.Update options/subresource/nil object")
	}
	return r.invoke(ctx, "update", obj.GetName(), obj, nil, func(input *unstructured.Unstructured) (*unstructured.Unstructured, error) {
		return r.base().Update(ctx, input, opts)
	})
}
func (r *dynamicResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	if !reflect.DeepEqual(opts, metav1.UpdateOptions{}) || obj == nil {
		return nil, unsupported("dynamic.UpdateStatus options/nil object")
	}
	return r.invoke(ctx, "status.update", obj.GetName(), obj, nil, func(input *unstructured.Unstructured) (*unstructured.Unstructured, error) {
		return r.base().UpdateStatus(ctx, input, opts)
	})
}

// Delete records a direct delete of the named object. Delete returns no
// object, so the call has no snapshots.
func (r *dynamicResource) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, sub ...string) error {
	if err := rejectDryRun("dynamic.Delete", opts.DryRun); err != nil {
		return err
	}
	if !reflect.DeepEqual(opts, metav1.DeleteOptions{}) || len(sub) != 0 {
		return unsupported("dynamic.Delete options/subresource")
	}
	if name == "" {
		return fmt.Errorf("dynamic effect requires an object name")
	}
	gvk, err := r.resolve()
	if err != nil {
		return err
	}
	return r.observe(ctx, gvk, "delete", name, nil, nil, func() (map[string]any, error) {
		return nil, r.base().Delete(ctx, name, opts)
	})
}
func (*dynamicResource) DeleteCollection(context.Context, metav1.DeleteOptions, metav1.ListOptions) error {
	return unsupported("dynamic.DeleteCollection")
}

// List records a direct list in the endpoint's namespace, with no name. An
// equality label selector is recorded as the labelSelector option. Other
// list options fail closed. Every listed item must match the endpoint.
func (r *dynamicResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if !reflect.DeepEqual(opts, metav1.ListOptions{LabelSelector: opts.LabelSelector}) {
		return nil, unsupported("dynamic.List options other than the label selector")
	}
	var recorded map[string]string
	if opts.LabelSelector != "" {
		parsed, err := labels.Parse(opts.LabelSelector)
		if err != nil {
			return nil, err
		}
		selector, err := equalitySelector(parsed)
		if err != nil {
			return nil, err
		}
		recorded = map[string]string{"labelSelector": selector}
	}
	gvk, err := r.resolve()
	if err != nil {
		return nil, err
	}
	var response *unstructured.UnstructuredList
	err = r.observe(ctx, gvk, "list", "", nil, recorded, func() (map[string]any, error) {
		var err error
		response, err = r.base().List(ctx, opts)
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, fmt.Errorf("dynamic list returned no list")
		}
		for _, item := range response.Items {
			if item.GroupVersionKind() != gvk || item.GetNamespace() != r.namespace {
				return nil, fmt.Errorf("dynamic list item %s/%s disagrees with endpoint", item.GetNamespace(), item.GetName())
			}
		}
		return response.DeepCopy().UnstructuredContent(), nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}
func (*dynamicResource) Watch(context.Context, metav1.ListOptions) (watch.Interface, error) {
	return nil, unsupported("dynamic.Watch transport")
}
func (*dynamicResource) Patch(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*unstructured.Unstructured, error) {
	return nil, unsupported("dynamic.Patch")
}
func (*dynamicResource) Apply(context.Context, string, *unstructured.Unstructured, metav1.ApplyOptions, ...string) (*unstructured.Unstructured, error) {
	return nil, unsupported("dynamic.Apply")
}
func (*dynamicResource) ApplyStatus(context.Context, string, *unstructured.Unstructured, metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return nil, unsupported("dynamic.ApplyStatus")
}

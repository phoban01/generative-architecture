package controllerruntime

import (
	"context"
	"fmt"
	"reflect"

	"generative-architecture/pkg/effect"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

// Dynamic observes direct dynamic-client reads and writes without a typed Scheme.
// The mapper must come from discovery or a checked catalog; plural names are never
// guessed. The supported operations are Get, Create, Update and UpdateStatus with
// zero options. Other methods fail closed instead of bypassing instrumentation.
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

func (r *dynamicResource) invoke(ctx context.Context, op, name string, obj *unstructured.Unstructured, execute func(*unstructured.Unstructured) (*unstructured.Unstructured, error)) (*unstructured.Unstructured, error) {
	if r.parent.base == nil || r.parent.mapper == nil || r.gvr.Version == "" || r.gvr.Resource == "" || name == "" {
		return nil, fmt.Errorf("dynamic effect requires client, mapper, exact GVR and object name")
	}
	gvk, err := r.parent.mapper.KindFor(r.gvr)
	if err != nil {
		return nil, err
	}
	mapping, err := r.parent.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}
	if mapping.Resource != r.gvr || gvk.Group != r.gvr.Group || gvk.Version != r.gvr.Version {
		return nil, fmt.Errorf("dynamic GVR/GVK mapping is not exact")
	}
	if (mapping.Scope.Name() == meta.RESTScopeNameNamespace && r.namespace == "") || (mapping.Scope.Name() == meta.RESTScopeNameRoot && r.namespace != "") {
		return nil, fmt.Errorf("dynamic namespace disagrees with resource scope")
	}
	d, err := r.parent.observer.Registry.LookupFor(r.parent.observer.Actor, effect.Resource{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind}, op, "direct")
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
	call := effect.Call{Descriptor: d, Key: effect.Key{Namespace: r.namespace, Name: name}, Submitted: submitted}
	run := func() effect.Result {
		var err error
		response, err = execute(input)
		if err != nil {
			return effect.Result{Err: err}
		}
		if response == nil || response.GroupVersionKind() != gvk || response.GetName() != name || response.GetNamespace() != r.namespace {
			return effect.Result{Err: fmt.Errorf("dynamic response identity disagrees with endpoint")}
		}
		return effect.Result{Snapshot: response.DeepCopy().Object}
	}
	var result effect.Result
	if r.parent.observer.Hook == nil {
		result = run()
	} else {
		result = r.parent.observer.Hook(ctx, call, run)
	}
	if result.Err != nil {
		return nil, result.Err
	}
	if response == nil {
		return nil, fmt.Errorf("dynamic hook returned success without executing request")
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
	return r.invoke(ctx, "get", name, nil, func(*unstructured.Unstructured) (*unstructured.Unstructured, error) {
		return r.base().Get(ctx, name, opts)
	})
}
func (r *dynamicResource) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, sub ...string) (*unstructured.Unstructured, error) {
	if !reflect.DeepEqual(opts, metav1.CreateOptions{}) || len(sub) != 0 || obj == nil {
		return nil, unsupported("dynamic.Create options/subresource/nil object")
	}
	return r.invoke(ctx, "create", obj.GetName(), obj, func(input *unstructured.Unstructured) (*unstructured.Unstructured, error) {
		return r.base().Create(ctx, input, opts)
	})
}
func (r *dynamicResource) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, sub ...string) (*unstructured.Unstructured, error) {
	if !reflect.DeepEqual(opts, metav1.UpdateOptions{}) || len(sub) != 0 || obj == nil {
		return nil, unsupported("dynamic.Update options/subresource/nil object")
	}
	return r.invoke(ctx, "update", obj.GetName(), obj, func(input *unstructured.Unstructured) (*unstructured.Unstructured, error) {
		return r.base().Update(ctx, input, opts)
	})
}
func (r *dynamicResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	if !reflect.DeepEqual(opts, metav1.UpdateOptions{}) || obj == nil {
		return nil, unsupported("dynamic.UpdateStatus options/nil object")
	}
	return r.invoke(ctx, "status.update", obj.GetName(), obj, func(input *unstructured.Unstructured) (*unstructured.Unstructured, error) {
		return r.base().UpdateStatus(ctx, input, opts)
	})
}
func (*dynamicResource) Delete(context.Context, string, metav1.DeleteOptions, ...string) error {
	return unsupported("dynamic.Delete")
}
func (*dynamicResource) DeleteCollection(context.Context, metav1.DeleteOptions, metav1.ListOptions) error {
	return unsupported("dynamic.DeleteCollection")
}
func (*dynamicResource) List(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return nil, unsupported("dynamic.List")
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

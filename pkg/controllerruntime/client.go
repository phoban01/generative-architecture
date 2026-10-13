// Package controllerruntime observes controller-runtime effects and provides
// bounded scheduling against its real worker and client-go FIFO queue.
package controllerruntime

import (
	"context"
	"fmt"
	"generative-architecture/pkg/effect"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Observer is test instrumentation, not a production authorization mechanism.
// Read consistency is an explicit property of the injected reader, not inferred.
type Observer struct {
	Actor    string
	Registry *effect.Registry
	Hook     effect.Hook
	Scheme   *runtime.Scheme
}

func (o Observer) invoke(ctx context.Context, operation, consistency string, key client.ObjectKey, obj client.Object, fn func() error) error {
	var gvk schema.GroupVersionKind
	var err error
	if u, ok := obj.(*unstructured.Unstructured); ok && u != nil {
		gvk = u.GroupVersionKind()
		if gvk.Version == "" || gvk.Kind == "" {
			return fmt.Errorf("unstructured observation requires explicit GVK")
		}
	} else {
		if o.Scheme == nil {
			return fmt.Errorf("typed observation requires a scheme")
		}
		gvk, err = apiutil.GVKForObject(obj, o.Scheme)
		if err != nil {
			return err
		}
	}
	d, err := o.Registry.LookupFor(o.Actor, effect.Resource{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind}, operation, consistency)
	if err != nil {
		return err
	}
	submitted, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj.DeepCopyObject())
	if err != nil {
		return err
	}
	call := effect.Call{Descriptor: d, Key: effect.Key{Namespace: key.Namespace, Name: key.Name}, Submitted: submitted}
	execute := func() effect.Result {
		err := fn()
		if err != nil {
			return effect.Result{Err: err}
		}
		snapshot, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj.DeepCopyObject())
		return effect.Result{Snapshot: snapshot, Err: err}
	}
	if o.Hook == nil {
		return execute().Err
	}
	return o.Hook(ctx, call, execute).Err
}
func unsupported(op string) error { return fmt.Errorf("unsupported controller-runtime effect: %s", op) }

// Reader intercepts Get. Unsupported operations fail closed.
func (o Observer) Reader(reader client.Reader, consistency string) client.Reader {
	return &readerProxy{base: reader, o: o, consistency: consistency}
}
func (o Observer) Client(base client.Client, consistency string) client.Client {
	return &clientProxy{Client: base, o: o, consistency: consistency}
}

type readerProxy struct {
	base        client.Reader
	o           Observer
	consistency string
}

func (r *readerProxy) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if len(opts) > 0 {
		return unsupported("Get options")
	}
	return r.o.invoke(ctx, "get", r.consistency, key, obj, func() error { return r.base.Get(ctx, key, obj) })
}
func (r *readerProxy) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return unsupported("List")
}

type clientProxy struct {
	client.Client
	o           Observer
	consistency string
}

func (c *clientProxy) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return (&readerProxy{base: c.Client, o: c.o, consistency: c.consistency}).Get(ctx, key, obj, opts...)
}
func (c *clientProxy) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return unsupported("List")
}
func (c *clientProxy) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if len(opts) > 0 {
		return unsupported("Update options")
	}
	return c.o.invoke(ctx, "update", "direct", client.ObjectKeyFromObject(obj), obj, func() error { return c.Client.Update(ctx, obj) })
}
func (c *clientProxy) Create(context.Context, client.Object, ...client.CreateOption) error {
	return unsupported("Create")
}
func (c *clientProxy) Delete(context.Context, client.Object, ...client.DeleteOption) error {
	return unsupported("Delete")
}
func (c *clientProxy) Patch(context.Context, client.Object, client.Patch, ...client.PatchOption) error {
	return unsupported("Patch")
}
func (c *clientProxy) DeleteAllOf(context.Context, client.Object, ...client.DeleteAllOfOption) error {
	return unsupported("DeleteAllOf")
}
func (c *clientProxy) Apply(context.Context, runtime.ApplyConfiguration, ...client.ApplyOption) error {
	return unsupported("Apply")
}
func (c *clientProxy) Status() client.SubResourceWriter { return c.SubResource("status") }
func (c *clientProxy) SubResource(name string) client.SubResourceClient {
	return &subresource{parent: c, name: name}
}

type subresource struct {
	parent *clientProxy
	name   string
}

func (s *subresource) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if s.name != "status" || len(opts) > 0 {
		return unsupported(s.name + ".Update/options")
	}
	return s.parent.o.invoke(ctx, "status.update", "direct", client.ObjectKeyFromObject(obj), obj, func() error { return s.parent.Client.Status().Update(ctx, obj) })
}
func (s *subresource) Get(context.Context, client.Object, client.Object, ...client.SubResourceGetOption) error {
	return unsupported(s.name + ".Get")
}
func (s *subresource) Create(context.Context, client.Object, client.Object, ...client.SubResourceCreateOption) error {
	return unsupported(s.name + ".Create")
}
func (s *subresource) Patch(context.Context, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
	return unsupported(s.name + ".Patch")
}
func (s *subresource) Apply(context.Context, runtime.ApplyConfiguration, ...client.SubResourceApplyOption) error {
	return unsupported(s.name + ".Apply")
}

// SplitClient combines an explicitly controlled reader with direct writes.
func SplitClient(base client.Client, reader client.Reader) client.Client {
	return &splitClient{Client: base, reader: reader}
}

type splitClient struct {
	client.Client
	reader client.Reader
}

func (c *splitClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return c.reader.Get(ctx, key, obj, opts...)
}
func (c *splitClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return c.reader.List(ctx, list, opts...)
}

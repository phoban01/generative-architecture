// Package controllerruntime observes controller-runtime effects and provides
// bounded scheduling against its real worker and client-go FIFO queue.
package controllerruntime

import (
	"context"
	"fmt"
	"generative-architecture/pkg/effect"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
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

// gvkFor resolves an object's identity. An unstructured object must carry an
// explicit GVK. A typed object needs the scheme.
func (o Observer) gvkFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	if u, ok := obj.(*unstructured.Unstructured); ok && u != nil {
		gvk := u.GroupVersionKind()
		if gvk.Version == "" || gvk.Kind == "" {
			return gvk, fmt.Errorf("unstructured observation requires explicit GVK")
		}
		return gvk, nil
	}
	if u, ok := obj.(*unstructured.UnstructuredList); ok && u != nil {
		gvk := u.GroupVersionKind()
		if gvk.Version == "" || gvk.Kind == "" {
			return gvk, fmt.Errorf("unstructured observation requires explicit GVK")
		}
		return gvk, nil
	}
	if o.Scheme == nil {
		return schema.GroupVersionKind{}, fmt.Errorf("typed observation requires a scheme")
	}
	return apiutil.GVKForObject(obj, o.Scheme)
}

// observe looks up the effect and runs fn through the hook. fn returns the
// detached snapshot of a successful call, or nil when the call has none.
func (o Observer) observe(ctx context.Context, gvk schema.GroupVersionKind, operation, consistency string, key client.ObjectKey, submitted map[string]any, options map[string]string, fn func() (map[string]any, error)) error {
	d, err := o.Registry.LookupFor(o.Actor, effect.Resource{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind}, operation, consistency)
	if err != nil {
		return err
	}
	call := effect.Call{Descriptor: d, Key: effect.Key{Namespace: key.Namespace, Name: key.Name}, Submitted: submitted, Options: options}
	execute := func() effect.Result {
		snapshot, err := fn()
		if err != nil {
			return effect.Result{Err: err}
		}
		return effect.Result{Snapshot: snapshot}
	}
	if o.Hook == nil {
		return execute().Err
	}
	return o.Hook(ctx, call, execute).Err
}

// invoke observes a call on one object. The object is the submitted value
// before the call and the snapshot after it.
func (o Observer) invoke(ctx context.Context, operation, consistency string, key client.ObjectKey, obj client.Object, options map[string]string, fn func() error) error {
	gvk, err := o.gvkFor(obj)
	if err != nil {
		return err
	}
	submitted, err := detach(obj)
	if err != nil {
		return err
	}
	return o.observe(ctx, gvk, operation, consistency, key, submitted, options, func() (map[string]any, error) {
		if err := fn(); err != nil {
			return nil, err
		}
		return detach(obj)
	})
}

func detach(obj runtime.Object) (map[string]any, error) {
	return runtime.DefaultUnstructuredConverter.ToUnstructured(obj.DeepCopyObject())
}

// rejectDryRun refuses a dry run by name. A dry run writes nothing, so
// recording it as a write would misstate the effect.
func rejectDryRun(op string, dryRun []string) error {
	if len(dryRun) > 0 {
		return fmt.Errorf("unsupported controller-runtime effect: %s with dry run %v: a dry run writes nothing and is not observed", op, dryRun)
	}
	return nil
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
	return r.o.invoke(ctx, "get", r.consistency, key, obj, nil, func() error { return r.base.Get(ctx, key, obj) })
}

// List records a list of the item kind at the reader's consistency. The key
// carries the namespace option and no name. An equality label selector is
// recorded as the labelSelector option. Other list options fail closed.
func (r *readerProxy) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	options := &client.ListOptions{}
	options.ApplyOptions(opts)
	if !reflect.DeepEqual(*options, client.ListOptions{Namespace: options.Namespace, LabelSelector: options.LabelSelector}) {
		return unsupported("List options other than the namespace and label selector")
	}
	var recorded map[string]string
	if options.LabelSelector != nil {
		selector, err := equalitySelector(options.LabelSelector)
		if err != nil {
			return err
		}
		recorded = map[string]string{"labelSelector": selector}
	}
	gvk, err := r.o.gvkFor(list)
	if err != nil {
		return err
	}
	item, ok := strings.CutSuffix(gvk.Kind, "List")
	if !ok || item == "" {
		return fmt.Errorf("list observation requires a list kind, got %s", gvk.Kind)
	}
	gvk.Kind = item
	return r.o.observe(ctx, gvk, "list", r.consistency, client.ObjectKey{Namespace: options.Namespace}, nil, recorded, func() (map[string]any, error) {
		if err := r.base.List(ctx, list, opts...); err != nil {
			return nil, err
		}
		return detach(list)
	})
}

// equalitySelector accepts the label selectors the list summary models:
// equality requirements only.
func equalitySelector(selector labels.Selector) (string, error) {
	requirements, selectable := selector.Requirements()
	if !selectable {
		return "", unsupported("List label selector that selects nothing")
	}
	for _, r := range requirements {
		if r.Operator() != selection.Equals && r.Operator() != selection.DoubleEquals {
			return "", unsupported(fmt.Sprintf("List label selector %q: only equality requirements are modelled", selector.String()))
		}
	}
	return selector.String(), nil
}

type clientProxy struct {
	client.Client
	o           Observer
	consistency string
}

func (c *clientProxy) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return (&readerProxy{base: c.Client, o: c.o, consistency: c.consistency}).Get(ctx, key, obj, opts...)
}
func (c *clientProxy) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return (&readerProxy{base: c.Client, o: c.o, consistency: c.consistency}).List(ctx, list, opts...)
}
func (c *clientProxy) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if len(opts) > 0 {
		return unsupported("Update options")
	}
	return c.o.invoke(ctx, "update", "direct", client.ObjectKeyFromObject(obj), obj, nil, func() error { return c.Client.Update(ctx, obj) })
}
func (c *clientProxy) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if len(opts) > 0 {
		return unsupported("Create options")
	}
	return c.o.invoke(ctx, "create", "direct", client.ObjectKeyFromObject(obj), obj, nil, func() error { return c.Client.Create(ctx, obj) })
}

// Delete records a direct delete keyed by the object. Both snapshots carry
// the object as the caller held it before deletion.
func (c *clientProxy) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	options := &client.DeleteOptions{}
	options.ApplyOptions(opts)
	if err := rejectDryRun("Delete", options.DryRun); err != nil {
		return err
	}
	if len(opts) > 0 {
		return unsupported("Delete options")
	}
	return c.o.invoke(ctx, "delete", "direct", client.ObjectKeyFromObject(obj), obj, nil, func() error { return c.Client.Delete(ctx, obj) })
}

// Patch records a direct patch, or apply for a server-side apply patch. The
// options carry the patch type and the patch bytes.
func (c *clientProxy) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	options := &client.PatchOptions{}
	options.ApplyOptions(opts)
	if err := rejectDryRun("Patch", options.DryRun); err != nil {
		return err
	}
	if len(opts) > 0 {
		return unsupported("Patch options")
	}
	operation, recorded, err := patchCall("", obj, patch)
	if err != nil {
		return err
	}
	return c.o.invoke(ctx, operation, "direct", client.ObjectKeyFromObject(obj), obj, recorded, func() error { return c.Client.Patch(ctx, obj, patch) })
}

// patchCall names a patch effect and records its type and bytes. A
// server-side apply patch is the apply operation, as in the summaries.
func patchCall(subresource string, obj client.Object, patch client.Patch) (string, map[string]string, error) {
	if patch == nil {
		return "", nil, unsupported("nil patch")
	}
	data, err := patch.Data(obj)
	if err != nil {
		return "", nil, err
	}
	operation := "patch"
	if patch.Type() == types.ApplyPatchType {
		operation = "apply"
	}
	if subresource != "" {
		operation = subresource + "." + operation
	}
	return operation, map[string]string{"patchType": string(patch.Type()), "patch": string(data)}, nil
}

// DeleteAllOf records a direct deleteAllOf keyed by the namespace option,
// with no name. It sends and returns no object, so it has no snapshots.
// Options other than the namespace fail closed.
func (c *clientProxy) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	options := &client.DeleteAllOfOptions{}
	options.ApplyOptions(opts)
	if err := rejectDryRun("DeleteAllOf", options.DryRun); err != nil {
		return err
	}
	if !reflect.DeepEqual(*options, client.DeleteAllOfOptions{ListOptions: client.ListOptions{Namespace: options.Namespace}}) {
		return unsupported("DeleteAllOf options other than the namespace")
	}
	gvk, err := c.o.gvkFor(obj)
	if err != nil {
		return err
	}
	return c.o.observe(ctx, gvk, "deleteAllOf", "direct", client.ObjectKey{Namespace: options.Namespace}, nil, nil, func() (map[string]any, error) {
		return nil, c.Client.DeleteAllOf(ctx, obj, opts...)
	})
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
	return s.parent.o.invoke(ctx, "status.update", "direct", client.ObjectKeyFromObject(obj), obj, nil, func() error { return s.parent.Client.Status().Update(ctx, obj) })
}
func (s *subresource) Get(context.Context, client.Object, client.Object, ...client.SubResourceGetOption) error {
	return unsupported(s.name + ".Get")
}
func (s *subresource) Create(context.Context, client.Object, client.Object, ...client.SubResourceCreateOption) error {
	return unsupported(s.name + ".Create")
}

// Patch records status.patch, or status.apply for a server-side apply patch.
// Other subresources fail closed.
func (s *subresource) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if s.name != "status" {
		return unsupported(s.name + ".Patch")
	}
	options := &client.SubResourcePatchOptions{}
	options.ApplyOptions(opts)
	if err := rejectDryRun("status.Patch", options.DryRun); err != nil {
		return err
	}
	if len(opts) > 0 {
		return unsupported("status.Patch options")
	}
	operation, recorded, err := patchCall(s.name, obj, patch)
	if err != nil {
		return err
	}
	return s.parent.o.invoke(ctx, operation, "direct", client.ObjectKeyFromObject(obj), obj, recorded, func() error { return s.parent.Client.Status().Patch(ctx, obj, patch) })
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

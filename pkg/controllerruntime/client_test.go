package controllerruntime_test

import (
	"context"
	"errors"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
)

func TestInterceptorUsesGVKKeysSnapshotsAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "test"}, Data: map[string]string{"value": "original"}}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	registry, err := effect.NewRegistry([]effect.Descriptor{{ID: "read", Resource: effect.Resource{Version: "v1", Kind: "ConfigMap"}, Operation: "get", Consistency: "cached"}})
	if err != nil {
		t.Fatal(err)
	}
	var observed effect.Result
	o := cr.Observer{Scheme: scheme, Registry: registry, Hook: func(ctx context.Context, c effect.Call, next func() effect.Result) effect.Result {
		if c.Key.Name != "source" || c.Key.Namespace != "test" || c.Descriptor.ID != "read" {
			t.Fatalf("unexpected call %+v", c)
		}
		observed = next()
		return observed
	}}
	wrapped := o.Client(base, "cached")
	read := &corev1.ConfigMap{}
	if err = wrapped.Get(ctx, client.ObjectKeyFromObject(obj), read); err != nil {
		t.Fatal(err)
	}
	read.Data["value"] = "mutated"
	if observed.Snapshot["data"].(map[string]any)["value"] != "original" {
		t.Fatal("snapshot aliases live object")
	}
	for name, fn := range map[string]func() error{
		"unregistered write": func() error { return wrapped.Update(ctx, read) },
		"list":               func() error { return wrapped.List(ctx, &corev1.ConfigMapList{}) },
		"create":             func() error { return wrapped.Create(ctx, obj) },
		"subresource bypass": func() error { return wrapped.SubResource("scale").Update(ctx, obj) },
		"patch bypass":       func() error { return wrapped.Status().Patch(ctx, obj, client.MergeFrom(obj.DeepCopy())) },
		"options":            func() error { return wrapped.Get(ctx, client.ObjectKeyFromObject(obj), read, &client.GetOptions{}) },
	} {
		t.Run(name, func(t *testing.T) {
			err := fn()
			if err == nil || !(strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), "unsummarized")) {
				t.Fatalf("operation escaped observation: %v", err)
			}
		})
	}
	injected := errors.New("injected")
	o.Hook = func(context.Context, effect.Call, func() effect.Result) effect.Result {
		return effect.Result{Err: injected}
	}
	if err = o.Reader(base, "cached").Get(ctx, client.ObjectKeyFromObject(obj), read); !errors.Is(err, injected) {
		t.Fatal(err)
	}
}

func TestTypedCreateIsObservedAndGated(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	registry, err := effect.NewRegistry([]effect.Descriptor{{ID: "create-mirror", Actor: "configmirror", Resource: effect.Resource{Version: "v1", Kind: "ConfigMap"}, Operation: "create", Consistency: "direct"}})
	if err != nil {
		t.Fatal(err)
	}
	var calls []effect.Call
	var results []effect.Result
	gate := make(chan struct{}, 1)
	o := cr.Observer{Actor: "configmirror", Scheme: scheme, Registry: registry, Hook: func(ctx context.Context, c effect.Call, next func() effect.Result) effect.Result {
		calls = append(calls, c)
		gate <- struct{}{}
		result := next()
		results = append(results, result)
		return result
	}}
	wrapped := o.Client(base, "cached")
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "mirror", Namespace: "test"}, Data: map[string]string{"value": "initial"}}
	if err := wrapped.Create(ctx, obj); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate:
	default:
		t.Fatal("create bypassed the gate hook")
	}
	if len(calls) != 1 || len(results) != 1 {
		t.Fatalf("expected one observed call, got %d calls and %d results", len(calls), len(results))
	}
	call := calls[0]
	if call.Descriptor.ID != "create-mirror" || call.Descriptor.Operation != "create" || call.Descriptor.Consistency != "direct" || call.Descriptor.Resource != (effect.Resource{Version: "v1", Kind: "ConfigMap"}) {
		t.Fatalf("wrong descriptor: %+v", call.Descriptor)
	}
	if call.Key != (effect.Key{Namespace: "test", Name: "mirror"}) {
		t.Fatalf("wrong key: %+v", call.Key)
	}
	if call.Submitted["data"].(map[string]any)["value"] != "initial" || call.Submitted["metadata"].(map[string]any)["resourceVersion"] != nil {
		t.Fatalf("submitted snapshot is not the pre-create object: %+v", call.Submitted)
	}
	obj.Data["value"] = "mutated"
	snapshot := results[0].Snapshot
	if snapshot["data"].(map[string]any)["value"] != "initial" {
		t.Fatal("snapshot aliases live object")
	}
	if snapshot["metadata"].(map[string]any)["resourceVersion"] == "" {
		t.Fatalf("snapshot lacks the server-assigned resourceVersion: %+v", snapshot["metadata"])
	}
	stored := &corev1.ConfigMap{}
	if err := base.Get(ctx, client.ObjectKeyFromObject(obj), stored); err != nil {
		t.Fatalf("object not created through the base client: %v", err)
	}
	if err := wrapped.Create(ctx, obj.DeepCopy(), &client.CreateOptions{}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("create options escaped observation: %v", err)
	}
	if len(calls) != 1 {
		t.Fatal("rejected create reached the hook")
	}
}

// recorder is a hook that keeps every call and its result.
type recorder struct {
	calls   []effect.Call
	results []effect.Result
}

func (r *recorder) hook(ctx context.Context, c effect.Call, next func() effect.Result) effect.Result {
	r.calls = append(r.calls, c)
	result := next()
	r.results = append(r.results, result)
	return result
}

func coreScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

// configMapRegistry declares one ConfigMap effect per operation/consistency
// pair. The pair is also the effect ID.
func configMapRegistry(t *testing.T, pairs ...[2]string) *effect.Registry {
	t.Helper()
	var entries []effect.Descriptor
	for _, p := range pairs {
		entries = append(entries, effect.Descriptor{ID: p[0] + "/" + p[1], Resource: effect.Resource{Version: "v1", Kind: "ConfigMap"}, Operation: p[0], Consistency: p[1]})
	}
	registry, err := effect.NewRegistry(entries)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func configMap(namespace, name string, labels map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}, Data: map[string]string{"value": name}}
}

// expectRejected fails unless err is a rejection whose message contains want.
func expectRejected(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected rejection containing %q, got %v", want, err)
	}
}

func TestTypedDeleteIsObserved(t *testing.T) {
	ctx := context.Background()
	scheme := coreScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap("test", "one", nil)).Build()
	rec := &recorder{}
	wrapped := cr.Observer{Scheme: scheme, Registry: configMapRegistry(t, [2]string{"delete", "direct"}), Hook: rec.hook}.Client(base, "cached")
	held := &corev1.ConfigMap{}
	if err := base.Get(ctx, client.ObjectKey{Namespace: "test", Name: "one"}, held); err != nil {
		t.Fatal(err)
	}
	if err := wrapped.Delete(ctx, held); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected one observed call, got %d", len(rec.calls))
	}
	call := rec.calls[0]
	if call.Descriptor.Operation != "delete" || call.Key != (effect.Key{Namespace: "test", Name: "one"}) {
		t.Fatalf("wrong call: %+v", call)
	}
	for name, snapshot := range map[string]map[string]any{"submitted": call.Submitted, "result": rec.results[0].Snapshot} {
		if snapshot["data"].(map[string]any)["value"] != "one" || snapshot["metadata"].(map[string]any)["resourceVersion"] == nil {
			t.Fatalf("%s snapshot is not the object before deletion: %+v", name, snapshot)
		}
	}
	if err := base.Get(ctx, client.ObjectKeyFromObject(held), &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
		t.Fatalf("object not deleted through the base client: %v", err)
	}
	if err := wrapped.Delete(ctx, held); !apierrors.IsNotFound(err) {
		t.Fatalf("API error not preserved: %v", err)
	}
	expectRejected(t, wrapped.Delete(ctx, held, client.DryRunAll), "Delete with dry run")
	expectRejected(t, wrapped.Delete(ctx, held, client.PropagationPolicy(metav1.DeletePropagationForeground)), "unsupported controller-runtime effect: Delete options")
	if len(rec.calls) != 2 {
		t.Fatal("rejected delete reached the hook")
	}
}

func TestTypedDeleteAllOfIsObserved(t *testing.T) {
	ctx := context.Background()
	scheme := coreScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap("test", "one", nil), configMap("test", "two", nil), configMap("other", "three", nil)).Build()
	rec := &recorder{}
	wrapped := cr.Observer{Scheme: scheme, Registry: configMapRegistry(t, [2]string{"deleteAllOf", "direct"}), Hook: rec.hook}.Client(base, "cached")
	if err := wrapped.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace("test")); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected one observed call, got %d", len(rec.calls))
	}
	call := rec.calls[0]
	if call.Descriptor.Operation != "deleteAllOf" || call.Descriptor.Consistency != "direct" || call.Key != (effect.Key{Namespace: "test"}) || call.Submitted != nil || rec.results[0].Snapshot != nil {
		t.Fatalf("wrong call: %+v, result %+v", call, rec.results[0])
	}
	remaining := &corev1.ConfigMapList{}
	if err := base.List(ctx, remaining); err != nil {
		t.Fatal(err)
	}
	if len(remaining.Items) != 1 || remaining.Items[0].Namespace != "other" {
		t.Fatalf("deleteAllOf did not reach the base client: %+v", remaining.Items)
	}
	if err := wrapped.DeleteAllOf(ctx, &corev1.ConfigMap{}); err != nil {
		t.Fatal(err)
	}
	if rec.calls[1].Key != (effect.Key{}) {
		t.Fatalf("empty options must give an empty key: %+v", rec.calls[1].Key)
	}
	expectRejected(t, wrapped.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace("test"), client.DryRunAll), "DeleteAllOf with dry run")
	expectRejected(t, wrapped.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.MatchingLabels{"app": "x"}), "DeleteAllOf options other than the namespace")
	if len(rec.calls) != 2 {
		t.Fatal("rejected deleteAllOf reached the hook")
	}
}

func TestTypedPatchIsObserved(t *testing.T) {
	ctx := context.Background()
	scheme := coreScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap("test", "one", nil)).Build()
	rec := &recorder{}
	wrapped := cr.Observer{Scheme: scheme, Registry: configMapRegistry(t, [2]string{"patch", "direct"}, [2]string{"apply", "direct"}), Hook: rec.hook}.Client(base, "cached")
	held := &corev1.ConfigMap{}
	if err := base.Get(ctx, client.ObjectKey{Namespace: "test", Name: "one"}, held); err != nil {
		t.Fatal(err)
	}
	original := held.DeepCopy()
	held.Data["value"] = "patched"
	if err := wrapped.Patch(ctx, held, client.MergeFrom(original)); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected one observed call, got %d", len(rec.calls))
	}
	call := rec.calls[0]
	if call.Descriptor.Operation != "patch" || call.Key != (effect.Key{Namespace: "test", Name: "one"}) {
		t.Fatalf("wrong call: %+v", call)
	}
	if call.Options["patchType"] != "application/merge-patch+json" || call.Options["patch"] != `{"data":{"value":"patched"}}` {
		t.Fatalf("wrong patch options: %+v", call.Options)
	}
	if rec.results[0].Snapshot["data"].(map[string]any)["value"] != "patched" {
		t.Fatalf("snapshot lacks the patched value: %+v", rec.results[0].Snapshot)
	}
	stored := &corev1.ConfigMap{}
	if err := base.Get(ctx, client.ObjectKeyFromObject(held), stored); err != nil || stored.Data["value"] != "patched" {
		t.Fatalf("patch did not reach the base client: %v %+v", err, stored.Data)
	}

	applied := configMap("test", "two", nil)
	applied.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}
	// The API requires a field manager for apply. The observer records the
	// call and preserves the API error.
	if err := wrapped.Patch(ctx, applied, client.Apply); !apierrors.IsInvalid(err) {
		t.Fatalf("API error not preserved: %v", err)
	}
	call = rec.calls[1]
	if call.Descriptor.Operation != "apply" || call.Options["patchType"] != "application/apply-patch+yaml" || !strings.Contains(call.Options["patch"], `"name":"two"`) {
		t.Fatalf("wrong apply call: %+v", call)
	}

	expectRejected(t, wrapped.Patch(ctx, held, client.MergeFrom(original), client.DryRunAll), "Patch with dry run")
	expectRejected(t, wrapped.Patch(ctx, held, client.MergeFrom(original), client.ForceOwnership), "Patch options")
	expectRejected(t, wrapped.SubResource("scale").Patch(ctx, held, client.MergeFrom(original)), "scale.Patch")
	if len(rec.calls) != 2 {
		t.Fatal("rejected patch reached the hook")
	}
}

func TestTypedListIsObserved(t *testing.T) {
	ctx := context.Background()
	scheme := coreScheme(t)
	selected := map[string]string{"app": "mirror"}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		configMap("test", "one", selected), configMap("test", "two", selected),
		configMap("test", "three", map[string]string{"app": "other"}), configMap("other", "four", selected),
	).Build()
	rec := &recorder{}
	o := cr.Observer{Scheme: scheme, Registry: configMapRegistry(t, [2]string{"list", "cached"}), Hook: rec.hook}
	list := &corev1.ConfigMapList{}
	if err := o.Reader(base, "cached").List(ctx, list, client.InNamespace("test"), client.MatchingLabels(selected)); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("expected two ConfigMaps, got %d", len(list.Items))
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected one observed call, got %d", len(rec.calls))
	}
	call := rec.calls[0]
	if call.Descriptor.ID != "list/cached" || call.Descriptor.Resource != (effect.Resource{Version: "v1", Kind: "ConfigMap"}) || call.Key != (effect.Key{Namespace: "test"}) || call.Submitted != nil {
		t.Fatalf("wrong call: %+v", call)
	}
	if call.Options["labelSelector"] != "app=mirror" {
		t.Fatalf("wrong list options: %+v", call.Options)
	}
	items := rec.results[0].Snapshot["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("snapshot is not the listed objects: %+v", rec.results[0].Snapshot)
	}
	list.Items[0].Data["value"] = "mutated"
	if items[0].(map[string]any)["data"].(map[string]any)["value"] == "mutated" {
		t.Fatal("snapshot aliases the live list")
	}

	unstructuredList := &unstructured.UnstructuredList{}
	unstructuredList.SetAPIVersion("v1")
	unstructuredList.SetKind("ConfigMapList")
	wrapped := o.Client(base, "cached")
	if err := wrapped.List(ctx, unstructuredList, client.InNamespace("test"), client.MatchingLabels(selected)); err != nil || len(unstructuredList.Items) != 2 {
		t.Fatalf("unstructured list: %v, %d items", err, len(unstructuredList.Items))
	}
	if len(rec.calls) != 2 || rec.calls[1].Descriptor.ID != "list/cached" {
		t.Fatalf("unstructured list not observed: %+v", rec.calls)
	}

	expectRejected(t, wrapped.List(ctx, list, client.MatchingFields{"metadata.name": "one"}), "List options other than the namespace and label selector")
	expectRejected(t, wrapped.List(ctx, list, client.Limit(1)), "List options other than the namespace and label selector")
	expectRejected(t, wrapped.List(ctx, list, client.HasLabels{"app"}), "only equality requirements are modelled")
	if len(rec.calls) != 2 {
		t.Fatal("rejected list reached the hook")
	}
}

func TestTypedStatusPatchIsObserved(t *testing.T) {
	ctx := context.Background()
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("provider.example/v1")
	obj.SetKind("Database")
	obj.SetName("one")
	obj.SetNamespace("demo")
	base := fake.NewClientBuilder().WithObjects(obj).WithStatusSubresource(obj).Build()
	identity := effect.Resource{Group: "provider.example", Version: "v1", Kind: "Database"}
	registry, err := effect.NewRegistry([]effect.Descriptor{{ID: "publish", Resource: identity, Operation: "status.patch", Consistency: "direct"}})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	wrapped := cr.Observer{Registry: registry, Hook: rec.hook}.Client(base, "direct")
	held := obj.DeepCopy()
	if err := unstructured.SetNestedField(held.Object, "db-1.internal", "status", "endpoint"); err != nil {
		t.Fatal(err)
	}
	if err := wrapped.Status().Patch(ctx, held, client.MergeFrom(obj)); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 || rec.calls[0].Descriptor.ID != "publish" || rec.calls[0].Options["patch"] != `{"status":{"endpoint":"db-1.internal"}}` {
		t.Fatalf("wrong status patch call: %+v", rec.calls)
	}
	stored := &unstructured.Unstructured{}
	stored.SetGroupVersionKind(obj.GroupVersionKind())
	if err := base.Get(ctx, client.ObjectKeyFromObject(obj), stored); err != nil {
		t.Fatal(err)
	}
	if endpoint, _, _ := unstructured.NestedString(stored.Object, "status", "endpoint"); endpoint != "db-1.internal" {
		t.Fatalf("status patch did not reach the base client: %+v", stored.Object)
	}
	expectRejected(t, wrapped.Status().Patch(ctx, held, client.MergeFrom(obj), client.DryRunAll), "status.Patch with dry run")
	if len(rec.calls) != 1 {
		t.Fatal("rejected status patch reached the hook")
	}
}

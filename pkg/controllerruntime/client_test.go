package controllerruntime_test

import (
	"context"
	"errors"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

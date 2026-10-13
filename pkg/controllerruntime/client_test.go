package controllerruntime_test

import (
	"context"
	"errors"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	corev1 "k8s.io/api/core/v1"
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

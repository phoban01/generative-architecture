package controllerruntime_test

import (
	"context"
	"errors"
	"testing"

	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/dynamicgen"
	"generative-architecture/pkg/effect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDynamicClientIdentitySnapshotsAndStatus(t *testing.T) {
	ctx := context.Background()
	binding := dynamicgen.Binding{Expression: "resource", Resource: effect.Resource{Group: "example.org", Version: "v1", Kind: "Person"}, Plural: "people", Scope: "Namespaced"}
	mapper, err := (dynamicgen.Catalog{Bindings: []dynamicgen.Binding{binding}}).Mapper()
	if err != nil {
		t.Fatal(err)
	}
	entries := []effect.Descriptor{}
	for _, op := range []string{"get", "create", "update", "status.update"} {
		entries = append(entries, effect.Descriptor{ID: op, Resource: binding.Resource, Operation: op, Consistency: "direct"})
	}
	registry, err := effect.NewRegistry(entries)
	if err != nil {
		t.Fatal(err)
	}
	base := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	var calls []effect.Call
	var results []effect.Result
	observer := cr.Observer{Registry: registry, Hook: func(ctx context.Context, c effect.Call, next func() effect.Result) effect.Result {
		calls = append(calls, c)
		result := next()
		results = append(results, result)
		return result
	}}
	dyn := observer.Dynamic(base, mapper)
	resource := dyn.Resource(binding.GVR()).Namespace("tenant")
	obj := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"value": "initial"}}}
	obj.SetGroupVersionKind(binding.GVK())
	obj.SetName("one")
	obj.SetNamespace("tenant")
	if _, err = resource.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	obj.Object["spec"].(map[string]any)["value"] = "mutated"
	if calls[0].Submitted["spec"].(map[string]any)["value"] != "initial" {
		t.Fatal("submitted snapshot aliases input")
	}
	fetched, err := resource.Get(ctx, "one", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = unstructured.SetNestedField(fetched.Object, "db-1.internal", "status", "endpoint"); err != nil {
		t.Fatal(err)
	}
	if _, err = resource.UpdateStatus(ctx, fetched, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	fetched.Object["status"].(map[string]any)["endpoint"] = "changed-after-call"
	if results[2].Snapshot["status"].(map[string]any)["endpoint"] != "db-1.internal" {
		t.Fatal("response snapshot aliases object")
	}
	actions := base.Actions()
	if len(actions) != 3 || actions[2].GetResource() != binding.GVR() || actions[2].GetSubresource() != "status" || actions[2].GetNamespace() != "tenant" {
		t.Fatalf("wrong API endpoint: %+v", actions)
	}
	for _, c := range calls {
		if c.Key.Namespace != "tenant" || c.Key.Name != "one" || c.Descriptor.Resource != binding.Resource {
			t.Fatalf("lost dynamic identity: %+v", c)
		}
	}
	bad := fetched.DeepCopy()
	bad.SetKind("Other")
	wrongNS := fetched.DeepCopy()
	wrongNS.SetNamespace("other")
	for name, fn := range map[string]func() error{
		"unknown GVR": func() error {
			_, err := dyn.Resource(schema.GroupVersionResource{Group: "example.org", Version: "v1", Resource: "persons"}).Namespace("tenant").Get(ctx, "one", metav1.GetOptions{})
			return err
		},
		"wrong GVK":         func() error { _, err := resource.UpdateStatus(ctx, bad, metav1.UpdateOptions{}); return err },
		"wrong namespace":   func() error { _, err := resource.Update(ctx, wrongNS, metav1.UpdateOptions{}); return err },
		"missing namespace": func() error { _, err := dyn.Resource(binding.GVR()).Get(ctx, "one", metav1.GetOptions{}); return err },
		"options":           func() error { _, err := resource.Get(ctx, "one", metav1.GetOptions{ResourceVersion: "7"}); return err },
		"unmodelled list":   func() error { _, err := resource.List(ctx, metav1.ListOptions{}); return err },
		"unmodelled watch":  func() error { _, err := resource.Watch(ctx, metav1.ListOptions{}); return err },
	} {
		t.Run(name, func(t *testing.T) {
			before := len(base.Actions())
			if err := fn(); err == nil {
				t.Fatal("unsupported effect accepted")
			}
			if len(base.Actions()) != before {
				t.Fatal("unsupported request reached API")
			}
		})
	}
	injected := errors.New("injected")
	observer.Hook = func(context.Context, effect.Call, func() effect.Result) effect.Result {
		return effect.Result{Err: injected}
	}
	before := len(base.Actions())
	_, err = observer.Dynamic(base, mapper).Resource(binding.GVR()).Namespace("tenant").Get(ctx, "one", metav1.GetOptions{})
	if !errors.Is(err, injected) || len(base.Actions()) != before {
		t.Fatal("injected error did not stop request")
	}
}

func TestUnstructuredControllerRuntimeClientNeedsGVKNotScheme(t *testing.T) {
	ctx := context.Background()
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("provider.example/v1")
	obj.SetKind("Database")
	obj.SetName("one")
	obj.SetNamespace("demo")
	base := fake.NewClientBuilder().WithObjects(obj).WithStatusSubresource(obj).Build()
	identity := effect.Resource{Group: "provider.example", Version: "v1", Kind: "Database"}
	registry, err := effect.NewRegistry([]effect.Descriptor{{ID: "read", Resource: identity, Operation: "get", Consistency: "direct"}, {ID: "publish", Resource: identity, Operation: "status.update", Consistency: "direct"}})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := cr.Observer{Registry: registry}.Client(base, "direct")
	read := &unstructured.Unstructured{}
	read.SetGroupVersionKind(obj.GroupVersionKind())
	if err = wrapped.Get(ctx, client.ObjectKeyFromObject(obj), read); err != nil {
		t.Fatal(err)
	}
	if err = unstructured.SetNestedField(read.Object, "db-1.internal", "status", "endpoint"); err != nil {
		t.Fatal(err)
	}
	if err = wrapped.Status().Update(ctx, read); err != nil {
		t.Fatal(err)
	}
	if err = wrapped.Get(ctx, client.ObjectKeyFromObject(obj), &unstructured.Unstructured{}); err == nil {
		t.Fatal("missing GVK accepted")
	}
}

// dynamicPeople builds a dynamic fake holding the given people and an
// observer whose registry has one direct effect per operation.
func dynamicPeople(t *testing.T, operations []string, people ...*unstructured.Unstructured) (dynamicgen.Binding, *dynamicfake.FakeDynamicClient, cr.Observer, *recorder) {
	t.Helper()
	binding := dynamicgen.Binding{Expression: "resource", Resource: effect.Resource{Group: "example.org", Version: "v1", Kind: "Person"}, Plural: "people", Scope: "Namespaced"}
	var entries []effect.Descriptor
	for _, op := range operations {
		entries = append(entries, effect.Descriptor{ID: op, Resource: binding.Resource, Operation: op, Consistency: "direct"})
	}
	registry, err := effect.NewRegistry(entries)
	if err != nil {
		t.Fatal(err)
	}
	base := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{binding.GVR(): "PersonList"})
	// Seed through the client: the fake tracker would guess the plural of
	// seeded objects.
	for _, p := range people {
		p.SetGroupVersionKind(binding.GVK())
		if _, err := base.Resource(binding.GVR()).Namespace(p.GetNamespace()).Create(context.Background(), p, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	base.ClearActions()
	rec := &recorder{}
	return binding, base, cr.Observer{Registry: registry, Hook: rec.hook}, rec
}

func person(namespace, name string, labels map[string]string) *unstructured.Unstructured {
	p := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"value": name}}}
	p.SetName(name)
	p.SetNamespace(namespace)
	p.SetLabels(labels)
	return p
}

func peopleMapper(t *testing.T, binding dynamicgen.Binding) meta.RESTMapper {
	t.Helper()
	mapper, err := (dynamicgen.Catalog{Bindings: []dynamicgen.Binding{binding}}).Mapper()
	if err != nil {
		t.Fatal(err)
	}
	return mapper
}

func TestDynamicListIsObserved(t *testing.T) {
	ctx := context.Background()
	selected := map[string]string{"app": "mirror"}
	binding, base, observer, rec := dynamicPeople(t, []string{"list"},
		person("tenant", "one", selected), person("tenant", "two", selected),
		person("tenant", "three", map[string]string{"app": "other"}), person("other", "four", selected))
	dyn := observer.Dynamic(base, peopleMapper(t, binding))
	resource := dyn.Resource(binding.GVR()).Namespace("tenant")
	list, err := resource.List(ctx, metav1.ListOptions{LabelSelector: "app=mirror"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("expected two people, got %d", len(list.Items))
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected one observed call, got %d", len(rec.calls))
	}
	call := rec.calls[0]
	if call.Descriptor.Operation != "list" || call.Key != (effect.Key{Namespace: "tenant"}) || call.Options["labelSelector"] != "app=mirror" || call.Submitted != nil {
		t.Fatalf("wrong call: %+v", call)
	}
	items := rec.results[0].Snapshot["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("snapshot is not the listed objects: %+v", rec.results[0].Snapshot)
	}
	list.Items[0].Object["spec"].(map[string]any)["value"] = "mutated"
	if items[0].(map[string]any)["spec"].(map[string]any)["value"] == "mutated" {
		t.Fatal("snapshot aliases the live list")
	}
	for name, fn := range map[string]func() error{
		"set-based selector": func() error {
			_, err := resource.List(ctx, metav1.ListOptions{LabelSelector: "app in (mirror)"})
			return err
		},
		"field selector": func() error {
			_, err := resource.List(ctx, metav1.ListOptions{FieldSelector: "metadata.name=one"})
			return err
		},
		"limit":          func() error { _, err := resource.List(ctx, metav1.ListOptions{Limit: 1}); return err },
		"all namespaces": func() error { _, err := dyn.Resource(binding.GVR()).List(ctx, metav1.ListOptions{}); return err },
	} {
		t.Run(name, func(t *testing.T) {
			before := len(base.Actions())
			if err := fn(); err == nil {
				t.Fatal("unsupported list accepted")
			}
			if len(base.Actions()) != before || len(rec.calls) != 1 {
				t.Fatal("unsupported list reached the API or the hook")
			}
		})
	}
}

func TestDynamicDeleteIsObserved(t *testing.T) {
	ctx := context.Background()
	binding, base, observer, rec := dynamicPeople(t, []string{"delete"}, person("tenant", "one", nil))
	resource := observer.Dynamic(base, peopleMapper(t, binding)).Resource(binding.GVR()).Namespace("tenant")
	if err := resource.Delete(ctx, "one", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected one observed call, got %d", len(rec.calls))
	}
	call := rec.calls[0]
	if call.Descriptor.Operation != "delete" || call.Key != (effect.Key{Namespace: "tenant", Name: "one"}) || call.Submitted != nil || rec.results[0].Snapshot != nil {
		t.Fatalf("wrong call: %+v", call)
	}
	actions := base.Actions()
	if len(actions) != 1 || actions[0].GetVerb() != "delete" || actions[0].GetNamespace() != "tenant" || actions[0].GetResource() != binding.GVR() {
		t.Fatalf("wrong API endpoint: %+v", actions)
	}
	if err := resource.Delete(ctx, "one", metav1.DeleteOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("API error not preserved: %v", err)
	}
	expectRejected(t, resource.Delete(ctx, "one", metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}}), "dynamic.Delete with dry run")
	expectRejected(t, resource.Delete(ctx, "one", metav1.DeleteOptions{}, "status"), "dynamic.Delete options/subresource")
	if err := resource.Delete(ctx, "", metav1.DeleteOptions{}); err == nil {
		t.Fatal("empty name accepted")
	}
	if len(rec.calls) != 2 || len(base.Actions()) != 2 {
		t.Fatal("rejected delete reached the hook or the API")
	}
}

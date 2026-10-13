package runtimecheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"generative-architecture/pkg/krm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"
)

func TestRuntimeProfileRejectsUnsupportedQueue(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0755); err != nil {
		t.Fatal(err)
	}
	sys, err := os.ReadFile("../../../system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "system.yaml"), sys, 0644); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../../runtime/controller-runtime.yaml")
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), "client-go-fifo", "priority", 1))
	if err = os.WriteFile(filepath.Join(root, "runtime/controller-runtime.yaml"), b, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(root); err == nil || !strings.Contains(err.Error(), "unsupported runtime profile") {
		t.Fatalf("got %v", err)
	}
}
func TestRecordedRuntimeConformance(t *testing.T) {
	if err := VerifyEvidence("../../.."); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob("../../../reports/runtime/trace-*.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 105 {
		t.Fatalf("run runtime-demo first: expected 105 traces, got %d", len(paths))
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			trace, err := ReadTrace(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Replay(trace); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestWholeMACReconcileWithStaleCache(t *testing.T) {
	h, err := newHarness()
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	for _, event := range []string{"enqueue", "start", "revoke"} {
		if err = h.apply(event); err != nil {
			t.Fatal(err)
		}
	}
	// Invoke the actual parent method within the real worker, with stale Client
	// reads and a direct APIReader. It must not publish the revoked machine.
	err = h.invoke(func() error { _, e := h.mac.Reconcile(h.ctx, request); return e })
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.projection()
	if err != nil {
		t.Fatal(err)
	}
	if got["cached"] != true || got["lease"] != false || got["published"] != false {
		t.Fatalf("unexpected state %v", got)
	}
	if err = h.apply("success"); err != nil {
		t.Fatal(err)
	}
}
func TestRealInformerDeliveryAndCacheLag(t *testing.T) {
	h, err := newHarness()
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	watcher := watch.NewRaceFreeFake()
	events := make(chan struct{}, 8)
	informer := cache.NewSharedIndexInformer(&classicListWatch{ListWatch: &cache.ListWatch{
		ListFunc:  func(metav1.ListOptions) (runtime.Object, error) { return &unstructured.UnstructuredList{}, nil },
		WatchFunc: func(metav1.ListOptions) (watch.Interface, error) { return watcher, nil },
	}}, &unstructured.Unstructured{}, 0, cache.Indexers{})
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: func(any) { events <- struct{}{} }, UpdateFunc: func(any, any) { events <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { defer close(stopped); informer.Run(ctx.Done()) }()
	defer func() { cancel(); <-stopped }()
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		t.Fatal("sync failed")
	}
	claim := &unstructured.Unstructured{}
	claim.SetGroupVersionKind(groupVersion.WithKind("MachineClaim"))
	if err = h.api.Get(ctx, request.NamespacedName, claim); err != nil {
		t.Fatal(err)
	}
	watcher.Add(claim.DeepCopy())
	wait := func() {
		t.Helper()
		select {
		case <-events:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	wait()
	if err = h.apply("revoke"); err != nil {
		t.Fatal(err)
	}
	stored, exists, err := informer.GetIndexer().GetByKey("demo/infra-customer")
	if err != nil || !exists {
		t.Fatal("missing cached claim", err)
	}
	machine, _, _ := unstructured.NestedString(stored.(*unstructured.Unstructured).Object, "status", "machineId")
	if machine != "host-1" {
		t.Fatal("cache changed without watch delivery")
	}
	if err = h.api.Get(ctx, request.NamespacedName, claim); err != nil {
		t.Fatal(err)
	}
	watcher.Modify(claim.DeepCopy())
	wait()
	stored, exists, err = informer.GetIndexer().GetByKey("demo/infra-customer")
	if err != nil || !exists {
		t.Fatal(err)
	}
	machine, _, _ = unstructured.NestedString(stored.(*unstructured.Unstructured).Object, "status", "machineId")
	if machine != "" {
		t.Fatal("delivered update not reflected in informer cache")
	}
}
func TestNativeDelayedQueueSurvivesForget(t *testing.T) {
	clock := clocktesting.NewFakeClock(time.Now())
	q := workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Second, time.Second), workqueue.TypedRateLimitingQueueConfig[string]{Clock: clock})
	defer q.ShutDown()
	q.AddAfter("key", 3*time.Second)
	q.Add("key")
	k, stop := q.Get()
	if stop || k != "key" {
		t.Fatal("missing immediate event")
	}
	q.Forget(k)
	q.Done(k)
	if q.Len() != 0 {
		t.Fatal("unexpected immediate retry")
	}
	clock.Step(3 * time.Second)
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for q.Len() == 0 {
		select {
		case <-deadline:
			t.Fatal("Forget incorrectly cancelled pending timer")
		case <-tick.C:
		}
	}
	k, stop = q.Get()
	if stop || k != "key" {
		t.Fatal("missing delayed event")
	}
	q.Done(k)
}
func TestIndependentObjectsHaveIndependentResourceVersions(t *testing.T) {
	h, err := newHarness()
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	// A lease write cannot cause a stale InfraMachine write to conflict: RVs are per object.
	before := h.cache.infra.DeepCopy()
	if err = h.apply("revoke"); err != nil {
		t.Fatal(err)
	}
	before.Status.MachineID = "host-1"
	if err = h.api.Status().Update(h.ctx, before); err != nil {
		t.Fatalf("cross-object RV assumption: %v", err)
	}
}
func TestReportIsKRM(t *testing.T) {
	var report map[string]any
	if err := krm.ReadResult("../../../reports/runtime/summary.yaml", "VerificationReport", &report); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(report["result"]) != "passed" {
		t.Fatal(report)
	}
}
func TestCachedAuthorizationMutationDetected(t *testing.T) {
	trace, err := ReadTrace("../../../reports/runtime/trace-staleCacheStep.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = replay(trace, true)
	if err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("cached authorization mutation escaped: %v", err)
	}
}

// This fake transport supplies classic list/watch, not streaming WatchList bookmarks.
type classicListWatch struct{ *cache.ListWatch }

func (*classicListWatch) IsWatchListSemanticsUnSupported() bool { return true }

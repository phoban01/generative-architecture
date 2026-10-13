package runtimecheck

import (
	"context"
	"fmt"
	cr "generative-architecture/pkg/controllerruntime"
	"reflect"
	"time"

	apiv1 "generative-architecture/examples/machinevendor/api/v1alpha1"
	"generative-architecture/examples/machinevendor/architecture"
	"generative-architecture/examples/machinevendor/controllerdemo"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var request = reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "demo", Name: "infra-customer"}}
var groupVersion = schema.GroupVersion{Group: "demo.architecture.local", Version: "v1alpha1"}

type response struct {
	result reconcile.Result
	err    error
}
type call struct {
	fn   func() error
	done chan error
}

// Snapshots retain the API resourceVersion, so stale status writes can conflict.
type snapshotReader struct {
	claim *apiv1.MachineClaim
	infra *apiv1.InfraMachine
}

func (c *snapshotReader) Get(_ context.Context, key client.ObjectKey, out client.Object, _ ...client.GetOption) error {
	if key != request.NamespacedName {
		return fmt.Errorf("unsupported cache key")
	}
	var value client.Object
	switch obj := out.(type) {
	case *apiv1.MachineClaim:
		c.claim.DeepCopyInto(obj)
		return nil
	case *apiv1.InfraMachine:
		c.infra.DeepCopyInto(obj)
		return nil
	case *unstructured.Unstructured:
		switch obj.GetKind() {
		case "MachineClaim":
			value = c.claim
		case "InfraMachine":
			value = c.infra
		default:
			return fmt.Errorf("unsupported cache kind")
		}
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(value)
		if err != nil {
			return err
		}
		gvk := obj.GroupVersionKind()
		obj.Object = m
		obj.SetGroupVersionKind(gvk)
		return nil
	default:
		return fmt.Errorf("unsupported cache type %T", out)
	}
}
func (c *snapshotReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return fmt.Errorf("List not summarized")
}

type harness struct {
	ctx        context.Context
	cancel     context.CancelFunc
	worker     *cr.Worker
	replies    chan response
	calls      chan call
	api        client.Client
	cache      *snapshotReader
	mac        *controllerdemo.MACReconciler
	observed   controllerdemo.Assignment
	accepted   bool
	processing bool
}

func newHarness() (*harness, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	h := &harness{ctx: ctx, cancel: cancel, replies: make(chan response), calls: make(chan call)}
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(groupVersion.WithKind("InfraMachine"), &apiv1.InfraMachine{})
	scheme.AddKnownTypeWithName(groupVersion.WithKind("MachineClaim"), &apiv1.MachineClaim{})
	meta := metav1.ObjectMeta{Name: request.Name, Namespace: request.Namespace}
	infra := &apiv1.InfraMachine{TypeMeta: metav1.TypeMeta{APIVersion: groupVersion.String(), Kind: "InfraMachine"}, ObjectMeta: meta, Spec: apiv1.InfraMachineSpec{AllocationRequested: true}}
	claim := &apiv1.MachineClaim{TypeMeta: metav1.TypeMeta{APIVersion: groupVersion.String(), Kind: "MachineClaim"}, ObjectMeta: meta, Status: apiv1.Assignment{MachineID: "host-1", Epoch: 1}}
	build := func() client.Client {
		return fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(infra, claim).WithObjects(infra.DeepCopy(), claim.DeepCopy()).Build()
	}
	h.api = build()
	h.cache = &snapshotReader{claim: &apiv1.MachineClaim{}, infra: &apiv1.InfraMachine{}}
	if err := h.api.Get(ctx, request.NamespacedName, h.cache.claim); err != nil {
		cancel()
		return nil, err
	}
	if err := h.api.Get(ctx, request.NamespacedName, h.cache.infra); err != nil {
		cancel()
		return nil, err
	}
	h.mac = &controllerdemo.MACReconciler{Client: cr.SplitClient(h.api, h.cache), APIReader: h.api}

	var err error
	h.worker, err = cr.NewWorker(ctx, request, 2, reconcile.Func(func(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
		for {
			select {
			case <-ctx.Done():
				return reconcile.Result{}, nil
			case c := <-h.calls:
				c.done <- c.fn()
			case reply := <-h.replies:
				return reply.result, reply.err
			}
		}
	}))
	if err != nil {
		cancel()
		return nil, err
	}
	return h, nil
}
func (h *harness) close() error { h.cancel(); return h.worker.Close() }

func (h *harness) invoke(fn func() error) error {
	c := call{fn: fn, done: make(chan error, 1)}
	select {
	case h.calls <- c:
	case <-h.ctx.Done():
		return h.ctx.Err()
	}
	select {
	case err := <-c.done:
		return err
	case <-h.ctx.Done():
		return h.ctx.Err()
	}
}
func (h *harness) apply(event string) error {
	switch event {
	case "enqueue":
		h.worker.Enqueue()
	case "start":
		if !h.worker.Ready() || h.processing {
			return fmt.Errorf("start without one ready key")
		}
		if err := h.worker.StartOne(); err != nil {
			return err
		}
		h.processing = true
	case "success", "error", "requeue", "after", "terminal", "error-with-after":
		if !h.processing {
			return fmt.Errorf("finish without active reconcile")
		}
		reply := response{}
		switch event {
		case "error":
			reply.err = fmt.Errorf("transient")
		case "requeue":
			reply.result.Requeue = true
		case "after":
			reply.result.RequeueAfter = 3 * time.Second
		case "terminal":
			reply.err = reconcile.TerminalError(fmt.Errorf("terminal"))
		case "error-with-after":
			reply.err = fmt.Errorf("transient")
			reply.result.RequeueAfter = 3 * time.Second
		}
		select {
		case h.replies <- reply:
		case <-h.ctx.Done():
			return h.ctx.Err()
		}
		if err := h.worker.Wait(); err != nil {
			return err
		}
		h.processing = false
	case "time":
		h.worker.Advance()
	case "fire":
		return h.worker.Fire()
	case "revoke":
		c := &apiv1.MachineClaim{}
		if err := h.api.Get(h.ctx, request.NamespacedName, c); err != nil {
			return err
		}
		c.Status.MachineID = ""
		return h.api.Status().Update(h.ctx, c)
	case "cache-sync":
		return h.api.Get(h.ctx, request.NamespacedName, h.cache.claim)
	case "infra-cache-sync":
		return h.api.Get(h.ctx, request.NamespacedName, h.cache.infra)
	case "observe":
		return h.invoke(func() error { v, err := h.mac.Observe(h.ctx, request.NamespacedName); h.observed = v; return err })
	case "publish":
		return h.invoke(func() error {
			v, err := h.mac.Publish(h.ctx, request.NamespacedName, h.observed)
			h.accepted = v
			if apierrors.IsConflict(err) {
				return nil
			}
			return err
		})
	case "idle":
	default:
		return fmt.Errorf("unknown runtime event %q", event)
	}
	return nil
}
func (h *harness) projection() (map[string]any, error) {
	live, cached := &apiv1.MachineClaim{}, &apiv1.MachineClaim{}
	infra := &unstructured.Unstructured{}
	infra.SetGroupVersionKind(groupVersion.WithKind("InfraMachine"))
	if err := h.api.Get(h.ctx, request.NamespacedName, live); err != nil {
		return nil, err
	}
	if err := h.cache.Get(h.ctx, request.NamespacedName, cached); err != nil {
		return nil, err
	}
	if err := h.api.Get(h.ctx, request.NamespacedName, infra); err != nil {
		return nil, err
	}
	machine, _, _ := unstructured.NestedString(infra.Object, "status", "machineId")
	now, due := h.worker.Clock()
	return map[string]any{"ready": h.worker.Ready(), "processing": h.processing, "due": due, "now": now, "retries": h.worker.Retries(), "lease": live.Status.MachineID != "", "cached": cached.Status.MachineID != "", "observed": h.observed.MachineID != "", "published": machine != "", "fresh": infra.GetResourceVersion() == h.cache.infra.ResourceVersion, "accepted": h.accepted}, nil
}

// Replay feeds only event names into the adapter. Expected state is used only
// for comparison. Dirty is private client-go state; checked through Done/Get effects.
func Replay(trace architecture.Trace) (int, error) { return replay(trace, false) }
func replay(trace architecture.Trace, cachedAuthorization bool) (int, error) {
	h, err := newHarness()
	if err != nil {
		return 0, err
	}
	defer h.close()
	if cachedAuthorization {
		h.mac.APIReader = h.mac.Client
	}
	for i, state := range trace.States {
		event, ok := state["event"].(string)
		if !ok {
			return i, fmt.Errorf("missing event")
		}
		if i > 0 {
			if err := h.apply(event); err != nil {
				return i, fmt.Errorf("step %d %s: %w", i, event, err)
			}
		}
		got, err := h.projection()
		if err != nil {
			return i, err
		}
		want, ok := state["s"].(map[string]any)
		if !ok {
			return i, fmt.Errorf("missing runtime state")
		}
		for k, v := range got {
			if !reflect.DeepEqual(want[k], v) {
				return i, fmt.Errorf("step %d %s: %s expected %v got %v", i, event, k, want[k], v)
			}
		}
	}
	return len(trace.States) - 1, nil
}

package configmirror

import (
	"context"
	"fmt"
	"generative-architecture/pkg/conformance"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

func Load(path string) (*model.Compiled, error) {
	var spec model.Spec
	if _, err := krm.Read(path, "TransitionModel", &spec); err != nil {
		return nil, err
	}
	return model.Compile(spec)
}

var sourceKey = client.ObjectKey{Namespace: "demo", Name: "source"}
var targetKey = client.ObjectKey{Namespace: "demo", Name: "copy"}

// Adapter supplies only fixtures, environment operations and projections.
// It receives events, never expected state. It does not implement copy logic.
type Adapter struct {
	api, cache                client.Client
	worker                    *cr.Worker
	gate                      *effect.Gate
	cancel                    context.CancelFunc
	snapshot                  string
	pc                        int64
	failed, processing, dirty bool
}

var _ conformance.Adapter = (*Adapter)(nil)

func New(ctx context.Context, registry *effect.Registry) (*Adapter, error) {
	return newAdapter(ctx, registry, "")
}
func newAdapter(ctx context.Context, registry *effect.Registry, mutation string) (*Adapter, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		cancel()
		return nil, err
	}
	source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: sourceKey.Namespace, Name: sourceKey.Name}, Data: map[string]string{"value": "a"}}
	target := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: targetKey.Namespace, Name: targetKey.Name}, Data: map[string]string{"value": ""}}
	a := &Adapter{api: fake.NewClientBuilder().WithScheme(scheme).WithObjects(source, target).Build(), cache: fake.NewClientBuilder().WithScheme(scheme).WithObjects(source.DeepCopy()).Build(), gate: effect.NewGate(), cancel: cancel}
	observer := cr.Observer{Registry: registry, Scheme: scheme, Hook: a.gate.Hook}
	reader := client.Reader(a.cache)
	if mutation == "direct-source" {
		reader = a.api
	}
	base := a.api
	if mutation == "wrong-write" {
		base = &wrongWrite{Client: base}
	}
	r := &Reconciler{Client: observer.Client(cr.SplitClient(base, reader), "cached"), APIReader: observer.Reader(a.api, "direct"), Target: targetKey}
	var err error
	a.worker, err = cr.NewWorker(ctx, reconcile.Request{NamespacedName: sourceKey}, 2, r)
	if err != nil {
		cancel()
		return nil, err
	}
	return a, nil
}

type wrongWrite struct{ client.Client }

func (w *wrongWrite) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	obj.(*corev1.ConfigMap).Data["value"] = "corrupt"
	return w.Client.Update(ctx, obj, opts...)
}
func (a *Adapter) Close() error { a.cancel(); return a.worker.Close() }
func value(ctx context.Context, c client.Reader, key client.ObjectKey) (string, error) {
	o := &corev1.ConfigMap{}
	err := c.Get(ctx, key, o)
	return o.Data["value"], err
}
func (a *Adapter) boundary(ctx context.Context, id string, fail bool) error {
	b, err := a.gate.Next(ctx)
	if err != nil {
		return err
	}
	if b.Call.Descriptor.ID != id {
		return fmt.Errorf("expected effect %s, got %s", id, b.Call.Descriptor.ID)
	}
	key := sourceKey
	if id != "readSource" {
		key = targetKey
	}
	if b.Call.Key != (effect.Key{Namespace: key.Namespace, Name: key.Name}) {
		return fmt.Errorf("unexpected object key %+v", b.Call.Key)
	}
	var injected error
	if fail {
		injected = fmt.Errorf("injected transient error")
	}
	result, err := b.Complete(ctx, injected)
	if err != nil {
		return err
	}
	if (result.Err != nil) != fail {
		return fmt.Errorf("unexpected API outcome: %v", result.Err)
	}
	a.failed = result.Err != nil
	if id == "readSource" && !fail {
		a.snapshot, _, _ = unstructured.NestedString(result.Snapshot, "data", "value")
	}
	return nil
}
func (a *Adapter) Apply(ctx context.Context, event string) error {
	switch event {
	case "changeSource":
		o := &corev1.ConfigMap{}
		if err := a.api.Get(ctx, sourceKey, o); err != nil {
			return err
		}
		if o.Data["value"] == "a" {
			o.Data["value"] = "b"
		} else {
			o.Data["value"] = "a"
		}
		return a.api.Update(ctx, o)
	case "deliverCache":
		live := &corev1.ConfigMap{}
		cached := &corev1.ConfigMap{}
		if err := a.api.Get(ctx, sourceKey, live); err != nil {
			return err
		}
		if err := a.cache.Get(ctx, sourceKey, cached); err != nil {
			return err
		}
		cached.Data = live.Data
		return a.cache.Update(ctx, cached)
	case "enqueue":
		a.worker.Enqueue()
		a.dirty = true
	case "begin":
		if err := a.worker.StartOne(); err != nil {
			return err
		}
		a.processing = true
		a.dirty = false
		a.pc = 1
		a.snapshot = ""
		a.failed = false
	case "readSource", "readSourceError":
		if err := a.boundary(ctx, "readSource", event == "readSourceError"); err != nil {
			return err
		}
		a.pc = 2
		if a.failed {
			a.pc = 4
		}
	case "readTarget", "readTargetError":
		if err := a.boundary(ctx, "readTarget", event == "readTargetError"); err != nil {
			return err
		}
		a.pc = 3
		if a.failed {
			a.pc = 4
		}
	case "commit", "writeError":
		if err := a.boundary(ctx, "writeTarget", event == "writeError"); err != nil {
			return err
		}
		a.pc = 4
	case "finish":
		if err := a.worker.Wait(); err != nil {
			return err
		}
		a.processing = false
		a.pc = 0
	case "advance":
		a.worker.Advance()
	case "fire":
		if err := a.worker.Fire(); err != nil {
			return err
		}
		a.dirty = true
	default:
		return fmt.Errorf("unknown event %s", event)
	}
	return nil
}
func (a *Adapter) Project(ctx context.Context) (map[string]any, error) {
	source, err := value(ctx, a.api, sourceKey)
	if err != nil {
		return nil, err
	}
	cached, err := value(ctx, a.cache, sourceKey)
	if err != nil {
		return nil, err
	}
	target, err := value(ctx, a.api, targetKey)
	if err != nil {
		return nil, err
	}
	now, due := a.worker.Clock()
	return map[string]any{"source": source, "cached": cached, "snapshot": a.snapshot, "target": target, "pc": a.pc, "failed": a.failed, "ready": a.worker.Ready(), "dirty": a.dirty, "processing": a.processing, "retries": a.worker.Retries(), "now": now, "due": due}, nil
}

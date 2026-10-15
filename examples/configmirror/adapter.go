package configmirror

import (
	"context"
	"fmt"
	"strings"
	"time"

	"generative-architecture/examples/configmirror/generated"
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

// Adapter supplies fixtures and runs the replay. It receives events, never
// expected state, and does not implement copy logic. Environment events,
// effect boundaries and the projection of API state are generated
// (generated/*_generated.go, see gen_test.go); the adapter routes events to
// them and drives the controller-runtime worker.
type Adapter struct {
	api, cache client.Client
	worker     *cr.Worker
	gate       *effect.Gate
	cancel     context.CancelFunc
	// The handwritten system.yaml observes the invocation through locals
	// the implementation has no field for; the adapter records them from
	// the worker events and the generated boundaries.
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

// workerEvents drive the controller-runtime worker and its queue.
var workerEvents = map[string]func(*Adapter) error{
	"enqueue": func(a *Adapter) error { a.worker.Enqueue(); a.dirty = true; return nil },
	"begin": func(a *Adapter) error {
		if err := a.worker.StartOne(); err != nil {
			return err
		}
		a.processing, a.dirty, a.pc, a.snapshot, a.failed = true, false, 1, "", false
		return nil
	},
	"finish": func(a *Adapter) error {
		if err := a.worker.Wait(); err != nil {
			return err
		}
		a.processing, a.pc = false, 0
		return nil
	},
	"advance": func(a *Adapter) error { a.worker.Advance(); return nil },
	"fire": func(a *Adapter) error {
		if err := a.worker.Fire(); err != nil {
			return err
		}
		a.dirty = true
		return nil
	},
}

// Apply routes event to the generated environment, the generated boundary
// gating or the worker.
func (a *Adapter) Apply(ctx context.Context, event string) error {
	if ok, err := generated.ApplyEnvironment(ctx, a.api, a.cache, event); ok {
		return err
	}
	b, ok, err := generated.ApplyBoundary(ctx, a.gate, event)
	if ok {
		if err != nil {
			return err
		}
		// The invocation moves to the next effect, or ends on an error or
		// after the last effect.
		a.failed = b.Result.Err != nil
		a.pc = b.Step + 1
		if a.failed {
			a.pc = generated.BoundarySteps + 1
		}
		if b.Effect == "readSource" && !a.failed {
			a.snapshot, _, _ = unstructured.NestedString(b.Result.Snapshot, "data", "value")
		}
		return nil
	}
	step, ok := workerEvents[event]
	if !ok {
		return fmt.Errorf("unknown event %s", event)
	}
	return step(a)
}

// modelFields name the generated projection's abstraction variables as
// system.yaml names its state.
var modelFields = map[string]string{
	"source": "ConfigMap_source_data_value",
	"cached": "cached_ConfigMap_source_data_value",
	"target": "ConfigMap_copy_data_value",
}

// Project reads API state through the generated projection and adds the
// worker's queue and clock and the recorded invocation locals.
func (a *Adapter) Project(ctx context.Context) (map[string]any, error) {
	api, err := generated.Project(ctx, a.api, a.cache)
	if err != nil {
		return nil, err
	}
	for name, v := range api {
		if strings.HasSuffix(name, "_exists") && v != true {
			return nil, fmt.Errorf("fixture object missing: %s", name)
		}
	}
	now, due := a.worker.Clock()
	state := map[string]any{"snapshot": a.snapshot, "pc": a.pc, "failed": a.failed, "ready": a.worker.Ready(), "dirty": a.dirty, "processing": a.processing, "retries": a.worker.Retries(), "now": now, "due": due}
	for field, variable := range modelFields {
		state[field] = api[variable]
	}
	return state, nil
}

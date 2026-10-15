package configmirror

import (
	"context"
	"fmt"
	"sort"
	"time"

	"generative-architecture/examples/configmirror/generated"
	"generative-architecture/pkg/conformance"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/model"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Load generates the model from the authored inputs in the example
// directory root (see Generate) and compiles it.
func Load(root string) (*model.Compiled, error) {
	spec, err := Generate(root)
	if err != nil {
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
	// observed are the model variables the adapter projects: the
	// generated projection's and the worker's that the model declares.
	observed []string
}

var _ conformance.Adapter = (*Adapter)(nil)

// New builds the fixtures and the worker for one replay of m: the source
// and copy ConfigMaps in a fake API, the source also in a separate cache
// that only environment deliveries update, and the reconciler wired as a
// controller-runtime manager wires it, its client reading through the
// cache and writing to the API, its APIReader reading the API.
func New(ctx context.Context, m *model.Compiled) (*Adapter, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		cancel()
		return nil, err
	}
	source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: sourceKey.Namespace, Name: sourceKey.Name}, Data: map[string]string{"value": "a"}}
	target := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: targetKey.Namespace, Name: targetKey.Name}, Data: map[string]string{"value": ""}}
	a := &Adapter{api: fake.NewClientBuilder().WithScheme(scheme).WithObjects(source, target).Build(), cache: fake.NewClientBuilder().WithScheme(scheme).WithObjects(source.DeepCopy()).Build(), gate: effect.NewGate(), cancel: cancel}
	observer := cr.Observer{Registry: m.Effects(), Scheme: scheme, Hook: a.gate.Hook}
	r := &Reconciler{Client: observer.Client(cr.SplitClient(a.api, a.cache), "cached"), APIReader: observer.Reader(a.api, "direct"), Target: targetKey}
	var err error
	a.worker, err = cr.NewWorker(ctx, reconcile.Request{NamespacedName: sourceKey}, 2, r)
	if err != nil {
		cancel()
		return nil, err
	}
	initial, err := m.Initial()
	if err == nil {
		var state map[string]any
		if state, err = a.project(ctx); err == nil {
			for name := range state {
				if _, declared := initial[name]; declared {
					a.observed = append(a.observed, name)
				}
			}
			sort.Strings(a.observed)
		}
	}
	if err != nil {
		a.Close()
		return nil, err
	}
	return a, nil
}

func (a *Adapter) Close() error { a.cancel(); return a.worker.Close() }

// Observed lists, in order, the model variables Project returns.
func (a *Adapter) Observed() []string { return append([]string(nil), a.observed...) }

// workerEvents drive the controller-runtime worker and its queue: the
// runtime profile's actions, as Generate names them.
var workerEvents = map[string]func(*Adapter) error{
	EventEnqueue:     func(a *Adapter) error { a.worker.Enqueue(); return nil },
	EventBegin:       func(a *Adapter) error { return a.worker.StartOne() },
	EventFinish:      func(a *Adapter) error { return a.worker.Wait() },
	EventFinishError: func(a *Adapter) error { return a.worker.Wait() },
	EventAdvance:     func(a *Adapter) error { a.worker.Advance(); return nil },
	EventFire:        func(a *Adapter) error { return a.worker.Fire() },
}

// Apply routes event to the generated environment, the generated boundary
// gating or the worker.
func (a *Adapter) Apply(ctx context.Context, event string) error {
	if ok, err := generated.ApplyEnvironment(ctx, a.api, a.cache, event); ok {
		return err
	}
	if _, ok, err := generated.ApplyBoundary(ctx, a.gate, event); ok {
		return err
	}
	if step, ok := workerEvents[event]; ok {
		return step(a)
	}
	// The controller takes its own branches, assignments and returns
	// between effect boundaries: the model's process steps need no driving.
	if _, ok := conformance.NodeID(event); ok {
		return nil
	}
	return fmt.Errorf("unknown event %s", event)
}

// Project returns the observed model variables: API and cache state through
// the generated projection, and the worker's queue and clock.
func (a *Adapter) Project(ctx context.Context) (map[string]any, error) {
	state, err := a.project(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(a.observed))
	for _, name := range a.observed {
		out[name] = state[name]
	}
	return out, nil
}

// project reads everything the adapter can observe, under the names of the
// generated projection and of the runtime profile.
func (a *Adapter) project(ctx context.Context) (map[string]any, error) {
	state, err := generated.Project(ctx, a.api, a.cache)
	if err != nil {
		return nil, err
	}
	now, due := a.worker.Clock()
	for name, v := range map[string]any{"ready": a.worker.Ready(), "dirty": a.worker.Dirty(), "processing": a.worker.Processing(), "retries": a.worker.Retries(), "now": now, "due": due} {
		state[name] = v
	}
	return state, nil
}

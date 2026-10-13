package consumer_test

import (
	"context"
	"generative-architecture/pkg/conformance"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/model"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
	"time"
)

func TestPublicPackagesFromAnotherModule(t *testing.T) {
	m, err := model.Compile(model.Spec{Name: "External", State: map[string]model.Variable{"done": {Type: "bool", Initial: "false"}}, Actions: []model.Action{{Name: "execute", Guard: "!done", Updates: map[string]expr.Expression{"done": "true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	actual := &adapter{}
	worker, err := cr.NewWorker(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "anything"}}, 2, reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
		actual.done = true
		return reconcile.Result{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	actual.worker = worker
	trace, err := conformance.Scenario(m, "execute")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conformance.Replay(ctx, m, trace, actual); err != nil {
		t.Fatal(err)
	}
}

type adapter struct {
	done   bool
	worker *cr.Worker
}

func (a *adapter) Apply(context.Context, string) error {
	a.worker.Enqueue()
	if err := a.worker.StartOne(); err != nil {
		return err
	}
	return a.worker.Wait()
}
func (a *adapter) Project(context.Context) (map[string]any, error) {
	return map[string]any{"done": a.done}, nil
}

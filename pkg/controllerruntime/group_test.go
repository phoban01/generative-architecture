package controllerruntime_test

import (
	"context"
	"generative-architecture/pkg/effect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"generative-architecture/pkg/conformance"
	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/model"

	"reflect"
	"testing"
	"time"
)

func groupProfile() cr.GroupProfile {
	return cr.GroupProfile{Name: "ManyControllers", MaxTicks: 20, Controllers: []cr.ModelController{{Name: "writer", Workers: 2, RetryTicks: 2, AfterTicks: 3, Keys: []cr.ModelKey{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}}}, {Name: "reader", Workers: 1, RetryTicks: 3, AfterTicks: 4, Keys: []cr.ModelKey{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}}}}}
}
func TestMultipleControllersWorkersAndKeys(t *testing.T) {
	m, err := cr.BuildGroupModel(groupProfile())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := model.Compile(m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	trace, err := conformance.Scenario(compiled, "writer_a_enqueue", "writer_b_enqueue", "writer_a_start", "writer_b_start", "writer_a_enqueue", "reader_a_enqueue", "reader_a_start", "writer_b_error", "writer_a_success", "reader_a_after", "writer_a_start", "writer_a_terminal", "advance", "advance", "writer_b_fire", "writer_b_start", "writer_b_success", "advance", "advance", "reader_a_fire", "reader_a_start", "reader_a_success")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err = cr.ReplayGroup(ctx, m, trace); err != nil {
		t.Fatal(err)
	}
}

func TestGroupProfileQueue(t *testing.T) {
	base, err := cr.BuildGroupModel(groupProfile())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, profile, controller string
		ok                        bool
	}{
		{name: "default", ok: true},
		{name: "fifo", profile: "fifo", ok: true},
		{name: "controller fifo", controller: "fifo", ok: true},
		{name: "unknown", profile: "lifo"},
		{name: "controller unknown", profile: "fifo", controller: "lifo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := groupProfile()
			p.Queue = tc.profile
			p.Controllers[0].Queue = tc.controller
			m, err := cr.BuildGroupModel(p)
			if !tc.ok {
				if err == nil {
					t.Fatal("unknown queue accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(m.Spec, base.Spec) {
				t.Fatal("fifo model differs from the default model")
			}
		})
	}
}

func TestInvocationEffectsRemainIsolated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	g := cr.NewGroup(ctx)
	defer g.Close()
	keys := []reconcile.Request{{NamespacedName: client.ObjectKey{Name: "a"}}, {NamespacedName: client.ObjectKey{Name: "b"}}}
	c, err := g.Add(cr.ControllerOptions{Name: "isolated", Workers: 2, Keys: keys, RetryTicks: 1, Reconciler: reconcile.Func(func(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		result := cr.InvocationHook(ctx, effect.Call{Descriptor: effect.Descriptor{ID: "read"}, Key: effect.Key{Name: req.Name}}, func() effect.Result { return effect.Result{Snapshot: map[string]any{"name": req.Name}} })
		return reconcile.Result{}, result.Err
	})})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if err := c.Enqueue(key); err != nil {
			t.Fatal(err)
		}
	}
	first, err := c.Start()
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Start()
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.Key == second.Key {
		t.Fatal("invocations were conflated")
	}
	// Complete in reverse order while both actual reconciler invocations are live.
	for _, inv := range []*cr.Invocation{second, first} {
		boundary, err := inv.Gate.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if boundary.Call.Invocation != inv.ID || boundary.Call.Key.Name != inv.Key.Name {
			t.Fatalf("wrong invocation: %+v", boundary.Call)
		}
		result, err := boundary.Complete(ctx, nil)
		if err != nil || result.Snapshot["name"] != inv.Key.Name {
			t.Fatalf("wrong result: %+v %v", result, err)
		}
		if _, actual, err := inv.Finish(); err != nil || actual != nil {
			t.Fatalf("finish: %v %v", actual, err)
		}
	}
	if c.Active() != 0 || c.Ready() != 0 {
		t.Fatal("worker did not complete native queue bookkeeping")
	}
	if result := cr.InvocationHook(ctx, effect.Call{}, nil); result.Err == nil {
		t.Fatal("uncontrolled effect accepted")
	}
}

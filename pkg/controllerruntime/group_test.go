package controllerruntime_test

import (
	"context"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/phoban01/generative-architecture/pkg/conformance"
	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/model"

	"fmt"
	"math/rand"
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

func TestLifecycleReplaysAgainstNativeQueue(t *testing.T) {
	p := mixedProfile()
	p.Name = "Lifecycle"
	for i := range p.Controllers {
		p.Controllers[i].Startup = true
		p.Controllers[i].Deletion = true
	}
	g, err := cr.BuildGroupModel(p)
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(g.Spec)
	if err != nil {
		t.Fatal(err)
	}
	scenarios := map[string][]string{
		"startup": {"writer_a_list", "writer_b_list", "writer_b_enqueue", "reader_b_list", "reader_a_list", "writer_sync", "reader_sync", "writer_b_start", "writer_a_start", "reader_b_start", "writer_a_tombstone", "writer_a_success", "writer_a_start", "writer_a_success", "writer_b_success", "reader_b_success", "reader_a_start", "reader_a_success"},
		// A trace that ends before sync shuts down cleanly.
		"unsynced": {"writer_a_list", "reader_a_tombstone"},
	}
	for i := range 3 {
		scenarios[fmt.Sprintf("sampled %d", i)] = sampleActions(t, g, m, int64(i), 80)
	}
	for name, actions := range scenarios {
		t.Run(name, func(t *testing.T) {
			trace, err := conformance.Scenario(m, actions...)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := cr.ReplayGroup(ctx, g, trace); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStartupWaitsForSync(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g := cr.NewGroup(ctx)
	defer g.Close()
	key := reconcile.Request{NamespacedName: client.ObjectKey{Name: "a"}}
	c, err := g.Add(cr.ControllerOptions{Name: "startup", Queue: cr.QueuePriority, Startup: true, Workers: 1, Keys: []reconcile.Request{key}, RetryTicks: 1, Reconciler: reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
		return reconcile.Result{}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnqueueLow(key); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(); err == nil {
		t.Fatal("started before sync")
	}
	if err := c.Sync(); err != nil {
		t.Fatal(err)
	}
	inv, err := c.Start()
	if err != nil {
		t.Fatal(err)
	}
	if inv.Priority != cr.LowPriority {
		t.Fatalf("initial-list key handed out at priority %d", inv.Priority)
	}
	if _, _, err := inv.Finish(); err != nil {
		t.Fatal(err)
	}
}

// mixedProfile runs a priority queue controller beside a FIFO controller.
func mixedProfile() cr.GroupProfile {
	p := groupProfile()
	p.Name = "MixedQueues"
	p.Controllers[0].Queue = cr.QueuePriority
	return p
}

// sampleActions walks the model from its initial state and picks an enabled
// action at random in each step.
func sampleActions(t *testing.T, g cr.GroupModel, m *model.Compiled, seed int64, n int) []string {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	s, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for range n {
		enabled := []string{}
		next := map[string]map[string]any{}
		for _, a := range g.Spec.Actions {
			if v, err := m.Next(s, a.Name); err == nil {
				enabled = append(enabled, a.Name)
				next[a.Name] = v
			}
		}
		if len(enabled) == 0 {
			break
		}
		pick := enabled[r.Intn(len(enabled))]
		out = append(out, pick)
		s = next[pick]
	}
	return out
}

func TestPriorityQueueReplaysAgainstNativeQueue(t *testing.T) {
	g, err := cr.BuildGroupModel(mixedProfile())
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(g.Spec)
	if err != nil {
		t.Fatal(err)
	}
	scenarios := map[string][]string{
		// A low-priority key waits behind a normal key enqueued later.
		"low after normal": {"writer_a_enqueueLow", "writer_b_enqueue", "writer_b_start", "writer_a_start", "writer_b_success", "writer_a_success"},
		// An add during processing takes its counter at add time. A retry
		// re-adds at the priority the key was taken with. An enqueue makes a
		// waiting item ready with its old counter. A fired timer takes a new one.
		"timers": {"writer_a_enqueueLow", "writer_a_start", "writer_a_enqueue", "writer_b_enqueue", "writer_a_error", "writer_a_start", "writer_b_start", "writer_b_requeue", "writer_a_after", "writer_a_enqueue", "writer_a_start", "writer_a_success", "advance", "advance", "writer_b_fire", "writer_b_start", "writer_b_errorAfter", "advance", "advance", "writer_b_fire", "writer_b_start", "writer_b_terminal"},
	}
	for i := range 4 {
		scenarios[fmt.Sprintf("sampled %d", i)] = sampleActions(t, g, m, int64(i), 80)
	}
	for name, actions := range scenarios {
		t.Run(name, func(t *testing.T) {
			trace, err := conformance.Scenario(m, actions...)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := cr.ReplayGroup(ctx, g, trace); err != nil {
				t.Fatal(err)
			}
		})
	}
}

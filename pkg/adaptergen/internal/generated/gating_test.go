package generated

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/adaptergen/internal/fixture"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/ir"
)

func TestGatingUpToDate(t *testing.T) {
	src, err := adaptergen.GenerateGating(fixture.Package, fixture.Process())
	if err != nil {
		t.Fatal(err)
	}
	assertGenerated(t, "gating.go", src)
}

// reach runs a controller side that reaches the gate with one effect call and
// returns what the hook handed back once the boundary is completed.
func reach(ctx context.Context, gate *effect.Gate, effectID string, result effect.Result) <-chan effect.Result {
	done := make(chan effect.Result, 1)
	go func() {
		done <- gate.Hook(ctx, effect.Call{Descriptor: effect.Descriptor{ID: effectID}, Key: effect.Key{Namespace: "demo", Name: "db-1"}}, func() effect.Result { return result })
	}()
	return done
}

func TestApplyProcessEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gate := effect.NewGate()
	nodes := fixture.Nodes()
	var observed []string
	observe := func(id string, r effect.Result) {
		observed = append(observed, id+":"+r.Snapshot["found"].(string))
	}

	for _, id := range []string{"n2", "n4", "n5", "n6"} {
		if err := ApplyProcessEvent(ctx, gate, nodes, id, observe); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if len(observed) != 0 {
		t.Fatalf("non-effect nodes observed %v", observed)
	}

	done := reach(ctx, gate, "readDatabase", effect.Result{Snapshot: map[string]any{"found": "yes"}})
	if err := ApplyProcessEvent(ctx, gate, nodes, "n1", observe); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.Err != nil || r.Snapshot["found"] != "yes" {
		t.Fatalf("controller saw %+v", r)
	}
	if strings.Join(observed, ",") != "n1:yes" {
		t.Fatalf("observed %v", observed)
	}

	failure := errors.New("instance read failed")
	done = reach(ctx, gate, "readInstance", effect.Result{Snapshot: map[string]any{"found": "no"}, Err: failure})
	if err := ApplyProcessEvent(ctx, gate, nodes, "n3", observe); !errors.Is(err, failure) {
		t.Fatalf("effect result error %v was not returned", err)
	}
	if r := <-done; !errors.Is(r.Err, failure) {
		t.Fatalf("controller saw %+v", r)
	}
	if strings.Join(observed, ",") != "n1:yes,n3:no" {
		t.Fatalf("observed %v", observed)
	}

	if err := ApplyProcessEvent(ctx, gate, nodes, "n9", observe); err == nil || !strings.Contains(err.Error(), "unknown process event") {
		t.Fatalf("unknown event: %v", err)
	}
	delete(nodes, "n7")
	if err := ApplyProcessEvent(ctx, gate, nodes, "n7", nil); err == nil || !strings.Contains(err.Error(), "unknown process event") {
		t.Fatalf("event missing from the loaded nodes: %v", err)
	}
}

func TestApplyProcessEventRejectsWrongBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gate := effect.NewGate()
	done := reach(ctx, gate, "createInstance", effect.Result{})
	err := ApplyProcessEvent(ctx, gate, fixture.Nodes(), "n3", nil)
	if err == nil || !strings.Contains(err.Error(), "expected effect readInstance, got createInstance") {
		t.Fatalf("wrong boundary: %v", err)
	}
	// The mismatched boundary stays pending until the invocation is cancelled.
	cancel()
	if r := <-done; !errors.Is(r.Err, context.Canceled) {
		t.Fatalf("controller saw %+v, want cancellation", r)
	}
}

func TestApplyProcessEventRejectsStaleNodes(t *testing.T) {
	ctx := context.Background()
	nodes := fixture.Nodes()
	nodes["n1"] = ir.Node{ID: "n1", Kind: ir.NodeBranch}
	nodes["n2"] = ir.Node{ID: "n2", Kind: ir.NodeEffect, Effect: "readDatabase"}
	nodes["n7"] = ir.Node{ID: "n7", Kind: ir.NodeEffect, Effect: "deleteInstance"}
	for id, want := range map[string]string{
		"n1": `generated for effect "readDatabase", loaded as branch ""`,
		"n2": `generated for branch "", loaded as effect "readDatabase"`,
		"n7": `generated for effect "createInstance", loaded as effect "deleteInstance"`,
	} {
		err := ApplyProcessEvent(ctx, effect.NewGate(), nodes, id, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", id, err, want)
		}
	}
}

func TestApplyProcessEventHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ApplyProcessEvent(ctx, effect.NewGate(), fixture.Nodes(), "n1", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("no controller reached the gate: %v, want cancellation", err)
	}
}

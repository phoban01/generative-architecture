package effect_test

import (
	"context"
	"github.com/phoban01/generative-architecture/pkg/effect"
	"testing"
	"time"
)

func TestAmbiguousBindingsRejected(t *testing.T) {
	a := effect.Descriptor{ID: "one", Resource: effect.Resource{Version: "v1", Kind: "Thing"}, Operation: "get", Consistency: "direct"}
	b := a
	b.ID = "two"
	if _, err := effect.NewRegistry([]effect.Descriptor{a, b}); err == nil {
		t.Fatal("accepted ambiguous lookup")
	}
}
func TestCancelledGateDoesNotExecute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g := effect.NewGate()
	done := make(chan effect.Result, 1)
	executed := false
	go func() {
		done <- g.Hook(ctx, effect.Call{}, func() effect.Result { executed = true; return effect.Result{} })
	}()
	if _, err := g.Next(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case result := <-done:
		if result.Err == nil || executed {
			t.Fatal("cancelled effect executed")
		}
	case <-time.After(time.Second):
		t.Fatal("gate leaked")
	}
}

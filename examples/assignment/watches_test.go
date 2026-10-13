package assignment

import (
	"context"
	"fmt"
	"generative-architecture/pkg/conformance"
	"os"
	"strings"
	"testing"
	"time"
)

func wiringSources(t *testing.T) ([]byte, string) {
	t.Helper()
	code, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatal(err)
	}
	w, err := os.ReadFile("wiring.go")
	if err != nil {
		t.Fatal(err)
	}
	return code, string(w)
}
func withPredicate(w, kind string) string {
	w = strings.Replace(w, "import (", "import (\n builder \"sigs.k8s.io/controller-runtime/pkg/builder\"\n predicate \"sigs.k8s.io/controller-runtime/pkg/predicate\"", 1)
	return strings.Replace(w, "handler.EnqueueRequestsFromMapFunc(ClaimToInfraMachine))", "handler.EnqueueRequestsFromMapFunc(ClaimToInfraMachine),builder.WithPredicates(predicate."+kind+"{}))", 1)
}
func eventsOf(trace conformance.Trace) []string {
	var events []string
	for _, s := range trace.States[1:] {
		events = append(events, s.Event)
	}
	return events
}
func firstAttempt(t *testing.T, e Example) []string {
	t.Helper()
	trace, err := e.Schedule("pending")
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, s := range trace.States[1:] {
		events = append(events, s.Event)
		if s.Values["finished"] == true && s.Values["invocations"] == int64(1) {
			return events
		}
	}
	t.Fatal("missing first return")
	return nil
}
func TestNoReconcileWithoutDeliveredWatch(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := e.Model.Initial()
	if _, err = e.Model.Next(state, "reconcile"); err == nil {
		t.Fatal("unsolicited reconcile allowed")
	}
	state, err = e.Model.Next(state, "discoverCompatible")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Model.Next(state, "reconcile"); err == nil {
		t.Fatal("write bypassed watch delivery")
	}
	state, err = e.Model.Next(state, "deliverInfra")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Model.Next(state, "reconcile"); err != nil {
		t.Fatal(err)
	}
}
func TestMissingWatchAndWrongMappingChangeModel(t *testing.T) {
	code, wiring := wiringSources(t)
	base, err := LoadSources(".", code, []byte(wiring))
	if err != nil {
		t.Fatal(err)
	}
	missing := strings.Replace(wiring, "Watches(&apiv1.MachineClaim{}, handler.EnqueueRequestsFromMapFunc(ClaimToInfraMachine)).\n\t\t", "", 1)
	wrong := strings.ReplaceAll(wiring, "claim.Spec.InfraMachineName", "claim.Name")
	for _, tc := range []struct {
		name, wiring, property string
		fulfill                bool
	}{{"missing-watch", missing, "claimFulfillmentEnqueuesMAC", true}, {"wrong-target", wrong, "watchTargetsInfraMachine", false}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wiring == wiring {
				t.Fatal("mutation did not change source")
			}
			mutated, err := LoadSources(".", code, []byte(tc.wiring))
			if err != nil {
				t.Fatal(err)
			}
			if mutated.Model.Quint() == base.Model.Quint() {
				t.Fatal("wiring change did not change model")
			}
			events := firstAttempt(t, base)
			if tc.fulfill {
				events = append(events, "fulfillClaim")
			}
			events = append(events, "deliverClaim")
			trace, err := conformance.Scenario(mutated.Model, events...)
			if err != nil {
				t.Fatal(err)
			}
			if err = mutated.CheckConstraints(trace); err == nil || !strings.Contains(err.Error(), tc.property) {
				t.Fatalf("constraint missed %s: %v", tc.name, err)
			}
		})
	}
}
func TestStatusPredicateMatchesNativeHandler(t *testing.T) {
	code, wiring := wiringSources(t)
	for _, tc := range []struct {
		predicate  string
		wantWakeup bool
	}{{"ResourceVersionChangedPredicate", true}, {"GenerationChangedPredicate", false}} {
		t.Run(tc.predicate, func(t *testing.T) {
			e, err := LoadSources(".", code, []byte(withPredicate(wiring, tc.predicate)))
			if err != nil {
				t.Fatal(err)
			}
			// Consume the creation event first, so an old queued key cannot mask a
			// missing wake-up from the subsequent status-only fulfillment update.
			pending, err := e.Schedule("pending")
			if err != nil {
				t.Fatal(err)
			}
			events := append(eventsOf(pending), "fulfillClaim", "deliverClaim")
			trace, err := conformance.Scenario(e.Model, events...)
			if err != nil {
				t.Fatal(err)
			}
			last := trace.States[len(trace.States)-1].Values
			if last["queued"] != tc.wantWakeup {
				t.Fatalf("wrong generated predicate behaviour: %v", last)
			}
			err = e.CheckConstraints(trace)
			if tc.wantWakeup && err != nil {
				t.Fatal(err)
			}
			if !tc.wantWakeup && (err == nil || !strings.Contains(err.Error(), "claimFulfillmentEnqueuesMAC")) {
				t.Fatalf("missed suppressed status update: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			a := New(ctx, e, false)
			defer a.Close()
			if _, err = conformance.ReplayObserved(ctx, e.Model, trace, a, Observed); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestEventDuringReconcileSurvivesReturn(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, event := range firstAttempt(t, e) {
		events = append(events, event)
		if strings.HasSuffix(event, "_createClaim") {
			events = append(events, "deliverClaim", "fulfillClaim", "deliverClaim")
		}
	}
	prefix, err := conformance.Scenario(e.Model, events...)
	if err != nil {
		t.Fatal(err)
	}
	state := prefix.States[len(prefix.States)-1].Values
	state, err = e.Model.Next(state, "reconcile")
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, "reconcile")
	for steps := 0; state["finished"] != true; steps++ {
		if steps > 64 {
			t.Fatal("process did not finish")
		}
		node := e.Program.Nodes[state["pc"].(int64)]
		action := fmt.Sprintf("node%d_%s", node.ID, node.Kind)
		state, err = e.Model.Next(state, action)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, action)
	}
	trace, err := conformance.Scenario(e.Model, events...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a := New(ctx, e, false)
	defer a.Close()
	if _, err = conformance.ReplayObserved(ctx, e.Model, trace, a, Observed); err != nil {
		t.Fatal(err)
	}
	if err = e.CheckConstraints(trace); err != nil {
		t.Fatal(err)
	}
	state, err = a.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state["statusMachineId"] != "machine-1" || state["invocations"] != int64(2) {
		t.Fatalf("dirty-key notification lost or duplicated: %v", state)
	}
}

func TestWatchAndDynamicResourceVersionsMustMatch(t *testing.T) {
	code, wiring := wiringSources(t)
	changed := strings.Replace(wiring, `Version: "v1alpha1"`, `Version: "v2"`, 1)
	if changed == wiring {
		t.Fatal("mutation missed source")
	}
	if _, err := LoadSources(".", code, []byte(changed)); err == nil || !strings.Contains(err.Error(), "watch identity") {
		t.Fatalf("different API versions were conflated: %v", err)
	}
}

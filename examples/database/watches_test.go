package database

import (
	"context"
	"fmt"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/model"
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
	return strings.Replace(w, "handler.EnqueueRequestsFromMapFunc(InstanceToDatabase))", "handler.EnqueueRequestsFromMapFunc(InstanceToDatabase),builder.WithPredicates(predicate."+kind+"{}))", 1)
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
	state, err = e.Model.Next(state, "createDb1_true")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Model.Next(state, "reconcile"); err == nil {
		t.Fatal("write bypassed watch delivery")
	}
	state, err = e.Model.Next(state, deliverDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Model.Next(state, "reconcile"); err != nil {
		t.Fatal(err)
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
			// missing wake-up from the subsequent status-only provisioning update.
			pending, err := e.Schedule("pending")
			if err != nil {
				t.Fatal(err)
			}
			events := append(eventsOf(pending), "changeInst1_1", deliverInstance)
			trace, err := conformance.Scenario(e.Model, events...)
			if err != nil {
				t.Fatal(err)
			}
			last := trace.States[len(trace.States)-1].Values
			if last["queued_demo_db_1"] != tc.wantWakeup {
				t.Fatalf("wrong generated predicate behaviour: %v", last)
			}
			err = e.CheckConstraints(trace)
			if tc.wantWakeup && err != nil {
				t.Fatal(err)
			}
			if !tc.wantWakeup && (err == nil || !strings.Contains(err.Error(), "instanceProvisioningEnqueuesReconciler")) {
				t.Fatalf("missed suppressed status update: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			a := New(ctx, e, false)
			defer a.Close()
			if _, err = conformance.ReplayObserved(ctx, e.Model, trace, a, e.Observed); err != nil {
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
		if strings.HasSuffix(event, "_createInstance") {
			events = append(events, deliverInstance, "changeInst1_1", deliverInstance)
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
	if _, err = conformance.ReplayObserved(ctx, e.Model, trace, a, e.Observed); err != nil {
		t.Fatal(err)
	}
	if err = e.CheckConstraints(trace); err != nil {
		t.Fatal(err)
	}
	state, err = a.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state["Database_db1_status_endpoint"] != "db-1.internal" || state["invocations"] != int64(2) {
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

// Every action that changes a watched object records its event, and only a
// create moves the generation here. The reference the instance mapping
// reads starts at the one value its abstraction domain allows.
func TestComposedWatchEvents(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]model.Action{}
	for _, a := range e.Spec.Actions {
		actions[a.Name] = a
		if a.Effect != "" {
			actions[a.Effect] = a
		}
	}
	for _, tc := range []struct {
		action, event, value string
		generation           bool
	}{
		{"createDb1_true", "databaseEvent", `"create"`, true},
		{"createDb1_false", "databaseEvent", `"create"`, true},
		{"createInstance", "databaseInstanceEvent", `"create"`, true},
		{"changeInst1_1", "databaseInstanceEvent", `databaseInstanceEvent == "" ? "update" : databaseInstanceEvent`, false},
		{"publish", "databaseEvent", `databaseEvent == "" ? "update" : databaseEvent`, false},
		{"removeDatabaseBeforeRead", "databaseEvent", `"delete"`, false},
	} {
		a, ok := actions[tc.action]
		if !ok {
			t.Fatalf("no action %s", tc.action)
		}
		if got := string(a.Updates[tc.event]); got != tc.value {
			t.Errorf("%s: %s = %q, want %q", tc.action, tc.event, got, tc.value)
		}
		generation := strings.TrimSuffix(tc.event, "Event") + "Generation"
		if _, moved := a.Updates[generation]; moved != tc.generation {
			t.Errorf("%s: moves %s = %v, want %v", tc.action, generation, moved, tc.generation)
		}
	}
	for _, name := range []string{"readDatabase", "readInstance", "checkContract"} {
		for v := range actions[name].Updates {
			if strings.HasSuffix(v, "Event") {
				t.Errorf("read %s raises %s", name, v)
			}
		}
	}
	reconcile := actions["reconcile"]
	if !strings.HasSuffix(string(reconcile.Guard), "&& queued_demo_db_1") || reconcile.Updates["queued_demo_db_1"] != "false" {
		t.Fatalf("reconcile does not pop the key: %v", reconcile)
	}
	if got := e.Spec.State["databaseInstance_Spec_DatabaseName"].Initial; got != `"db-1"` {
		t.Fatalf("instance reference starts at %s", got)
	}
}

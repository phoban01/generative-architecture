package controllerruntime_test

import (
	"testing"

	cr "generative-architecture/pkg/controllerruntime"
	"generative-architecture/pkg/model"
)

func priorityProfile() cr.GroupProfile {
	return cr.GroupProfile{Name: "Priority", MaxTicks: 6, Queue: cr.QueuePriority, Controllers: []cr.ModelController{{Name: "c", Workers: 1, RetryTicks: 2, AfterTicks: 3, Keys: []cr.ModelKey{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}}}}}
}

// steps applies actions from the initial state and fails on a disabled one.
func steps(t *testing.T, m *model.Compiled, actions ...string) map[string]any {
	t.Helper()
	s, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions {
		if s, err = m.Next(s, a); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
		if err = m.Check(s, a); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	return s
}

func compiledPriority(t *testing.T) *model.Compiled {
	t.Helper()
	g, err := cr.BuildGroupModel(priorityProfile())
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.Compile(g.Spec)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPriorityModelStartsNormalBeforeLow(t *testing.T) {
	m := compiledPriority(t)
	s := steps(t, m, "c_a_enqueueLow", "c_b_enqueue")
	if _, err := m.Next(s, "c_a_start"); err == nil {
		t.Fatal("low-priority key started before a normal key enqueued later")
	}
	s = steps(t, m, "c_a_enqueueLow", "c_b_enqueue", "c_b_start")
	if s["c_b_taken"] != int64(0) {
		t.Fatalf("taken priority %v", s["c_b_taken"])
	}
	// A normal add raises a low item to normal priority with a new counter.
	s = steps(t, m, "c_a_enqueueLow", "c_b_enqueue", "c_a_enqueue")
	if _, err := m.Next(s, "c_a_start"); err == nil {
		t.Fatal("raised key overtook the earlier normal key")
	}
	if s["c_a_priority"] != int64(0) || s["c_a_order"] != int64(2) {
		t.Fatalf("raise: priority %v order %v", s["c_a_priority"], s["c_a_order"])
	}
}

func TestPriorityModelOrdersAddsDuringProcessing(t *testing.T) {
	m := compiledPriority(t)
	// An add during processing takes its counter at add time, not at Done.
	s := steps(t, m, "c_a_enqueue", "c_a_start", "c_a_enqueue", "c_b_enqueue", "c_a_success")
	if _, err := m.Next(s, "c_b_start"); err == nil {
		t.Fatal("b overtook a, which was added first during processing")
	}
	// An error re-adds the key with the priority it was taken with.
	s = steps(t, m, "c_a_enqueueLow", "c_a_start", "c_a_error")
	if s["c_a_priority"] != int64(cr.LowPriority) || s["c_a_due"] != int64(2) || s["c_a_retries"] != int64(1) {
		t.Fatalf("retry: %v %v %v", s["c_a_priority"], s["c_a_due"], s["c_a_retries"])
	}
	// An add without delay makes a waiting item ready and keeps its counter.
	s = steps(t, m, "c_a_enqueue", "c_a_start", "c_a_error", "c_b_enqueue", "c_a_enqueue")
	if s["c_a_due"] != int64(-1) || s["c_a_queued"] != true || s["c_a_order"] != int64(1) {
		t.Fatalf("promote: %v %v %v", s["c_a_due"], s["c_a_queued"], s["c_a_order"])
	}
	if _, err := m.Next(s, "c_b_start"); err == nil {
		t.Fatal("promoted key lost its counter")
	}
	// A timer that fires takes a new counter.
	s = steps(t, m, "c_a_enqueue", "c_a_start", "c_a_error", "c_b_enqueue", "advance", "advance", "c_a_fire")
	if s["c_a_order"] != int64(3) {
		t.Fatalf("fire order %v", s["c_a_order"])
	}
}

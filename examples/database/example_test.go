package database

import (
	"context"
	"github.com/phoban01/generative-architecture/pkg/conformance"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/report"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFullProvisioningFlow(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, outcome, id string
		creates           int64
	}{{"absent", "absent", "", 0}, {"unsupported", "unsupported", "", 0}, {"pending", "waiting", "", 1}, {"provisioned", "ready", "db-1.internal", 1}, {"repeated", "ready", "db-1.internal", 1}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			trace, err := e.Schedule(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.CheckConstraints(trace); err != nil {
				t.Fatal(err)
			}
			a := New(ctx, e, false)
			defer a.Close()
			report, err := conformance.ReplayObserved(ctx, e.Model, trace, a, e.Observed)
			if err != nil {
				t.Fatal(err)
			}
			state, err := a.Project(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if state["outcome"] != tc.outcome || state["instanceCreates"] != tc.creates || state["Database_db1_status_endpoint"] != tc.id {
				t.Fatalf("unexpected Go result: %v", state)
			}
			if tc.name == "absent" || tc.name == "unsupported" {
				for event := range report.Coverage {
					if strings.HasSuffix(event, "_readInstance") || strings.HasSuffix(event, "_createInstance") || strings.HasSuffix(event, "_publish") {
						t.Fatalf("ineligible Database touched instance/status: %s", event)
					}
				}
			}
		})
	}
}
func TestReplayDetectsWrongStatusWrite(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	trace, err := e.Schedule("provisioned")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a := New(ctx, e, true)
	defer a.Close()
	if _, err = conformance.ReplayObserved(ctx, e.Model, trace, a, e.Observed); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("missed corrupt status write: %v", err)
	}
}
func TestRecordedQuintSchedules(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	report, err := report.ReadVerification("report.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if report.Result != "passed" || report.ModelSHA256 != goflow.Hash([]byte(e.Constrained.Quint())) {
		t.Fatal("stale evidence; run walkthrough --refresh")
	}
	if report.Mode != "strict" || len(report.Failures) != 4 || len(report.InputHashes) == 0 {
		t.Fatalf("report mode %q failures %v inputs %d", report.Mode, report.Failures, len(report.InputHashes))
	}
	for file, want := range report.InputHashes {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if goflow.Hash(b) != want {
			t.Fatal("inputs changed; run walkthrough --refresh")
		}
	}
	paths, err := filepath.Glob("traces/*.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 40 || len(paths) != report.Traces {
		t.Fatal("expected 40 saved Quint traces")
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if goflow.Hash(b) != report.TraceHashes[filepath.Base(path)] {
			t.Fatal("trace changed")
		}
		trace, err := conformance.ReadITF(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = e.CheckConstraints(trace); err != nil {
			t.Fatal(err)
		}
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			a := New(ctx, e, false)
			defer a.Close()
			if _, err = conformance.ReplayObserved(ctx, e.Model, trace, a, e.Observed); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}()
	}
}

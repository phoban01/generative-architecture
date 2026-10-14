package database

import (
	"context"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/krm"
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
			report, err := conformance.ReplayObserved(ctx, e.Model, trace, a, Observed)
			if err != nil {
				t.Fatal(err)
			}
			state, err := a.Project(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if state["outcome"] != tc.outcome || state["instanceCreates"] != tc.creates || state["statusEndpoint"] != tc.id {
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
func TestSourceMutationsViolateUnchangedConstraints(t *testing.T) {
	source, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatal(err)
	}
	original, err := LoadSource(".", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct{ name, from, to, scenario, property string }{
		{"contract-check", "if !compatible", "if compatible", "unsupported", "instancesRequireCompatibleDatabase"},
		{"provisioning-check", `if endpoint == ""`, `if endpoint != ""`, "pending", "readyHasEndpoint"},
		{"wrong-id", "store.UpdateDatabaseStatus(endpoint)", `store.UpdateDatabaseStatus("wrong-endpoint")`, "provisioned", "publicationRequiresProvisionedInstance"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			changed := strings.Replace(string(source), mutation.from, mutation.to, 1)
			if changed == string(source) {
				t.Fatal("mutation did not change source")
			}
			e, err := LoadSource(".", []byte(changed))
			if err != nil {
				t.Fatal(err)
			}
			if e.Model.Quint() == original.Model.Quint() {
				t.Fatal("source mutation did not change generated model")
			}
			trace, err := e.Schedule(mutation.scenario)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.CheckConstraints(trace); err == nil || !strings.Contains(err.Error(), mutation.property) {
				t.Fatalf("independent constraint missed mutation: %v", err)
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
	if _, err = conformance.ReplayObserved(ctx, e.Model, trace, a, Observed); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("missed corrupt status write: %v", err)
	}
}
func TestRecordedQuintSchedules(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Result      string            `yaml:"result"`
		Traces      int               `yaml:"traces"`
		Transitions int               `yaml:"transitions"`
		Coverage    map[string]int    `yaml:"coverage"`
		TraceHashes map[string]string `yaml:"traceHashes"`
		InputHashes map[string]string `yaml:"inputHashes"`
		ModelSHA256 string            `yaml:"modelSha256"`
		Scope       string            `yaml:"scope"`
	}
	if err = krm.ReadResult("report.yaml", "VerificationReport", &report); err != nil {
		t.Fatal(err)
	}
	if report.Result != "passed" || report.ModelSHA256 != goflow.Hash([]byte(e.Constrained.Quint())) {
		t.Fatal("stale evidence; run walkthrough --refresh")
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
			if _, err = conformance.ReplayObserved(ctx, e.Model, trace, a, Observed); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}()
	}
}

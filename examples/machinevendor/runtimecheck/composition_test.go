package runtimecheck

import (
	"generative-architecture/examples/machinevendor/behaviorgen"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposedRecordedTraces(t *testing.T) {
	if err := VerifyCompositionEvidence("../../.."); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob("../../../reports/composition/*.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 110 {
		t.Fatalf("expected 109 traces plus counterexample; got %d", len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			trace, err := ReadCompositionTrace(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ReplayComposition(trace); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestComposedLiveLeaseRaceRetainsSnapshot(t *testing.T) {
	trace, err := ReadCompositionTrace("../../../reports/composition/live-lease-race.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	last := trace.States[len(trace.States)-1]
	s := last["s"].(map[string]any)
	if trace.Status != "violation" || last["event"] != "commit" || s["lease"] != false || s["cached"] != false || last["publishedMachineId"] != "host-1" {
		t.Fatalf("unexpected race endpoint: %v", last)
	}
	if last["claim"].(map[string]any)["machineId"] != "host-1" || last["observed"].(map[string]any)["machineId"] != "host-1" {
		t.Fatal("in-flight snapshots were overwritten by cache delivery")
	}
	if _, err = ReplayComposition(trace); err != nil {
		t.Fatal(err)
	}
}
func TestComposedConflictRetriesAndRecovers(t *testing.T) {
	trace, err := ReadCompositionTrace("../../../reports/composition/trace-conflictRetryStep.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	conflict, retry, fired, recovered := false, false, false, false
	for _, state := range trace.States {
		s := state["s"].(map[string]any)
		switch state["event"] {
		case "writeError":
			if state["injected"] == false {
				conflict = true
			}
		case "finish":
			if state["returnedError"] == true && s["retries"] == int64(1) {
				retry = true
			}
		case "fire":
			fired = true
		case "commit":
			if fired {
				recovered = true
			}
		}
	}
	if !conflict || !retry || !fired || !recovered {
		t.Fatalf("missing conflict/retry/recovery: %v %v %v %v", conflict, retry, fired, recovered)
	}
	if _, err = ReplayComposition(trace); err != nil {
		t.Fatal(err)
	}
}
func TestComposedMutationsDetected(t *testing.T) {
	trace, err := ReadCompositionTrace("../../../reports/composition/trace-staleStep.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"cached-authorization", "skip-revalidation"} {
		t.Run(mutation, func(t *testing.T) {
			if _, err := replayComposition(trace, mutation); err == nil || !strings.Contains(err.Error(), "expected") {
				t.Fatalf("mutation escaped or unrelated error: %v", err)
			}
		})
	}
}
func TestCompositionUsesExtractedReturnPolicy(t *testing.T) {
	_, p, profile, err := loadComposition("../../..")
	if err != nil {
		t.Fatal(err)
	}
	cfg := p.Selection()
	src, err := os.ReadFile(filepath.Join("../../..", cfg.Source))
	if err != nil {
		t.Fatal(err)
	}
	// Change ONLY Publish's final return, preserving helper bodies and the real source on disk.
	source := string(src)
	start := strings.Index(source, "func (r *MACReconciler) Publish(")
	if start < 0 {
		t.Fatal("method missing")
	}
	end := strings.Index(source[start:], "// +arch:action=clear")
	if end < 0 {
		t.Fatal("method boundary missing")
	}
	end += start
	changed := source[:start] + strings.Replace(source[start:end], "return err == nil, err", "return err == nil, nil", 1) + source[end:]
	mutant, err := behaviorgen.Extract([]byte(changed), cfg)
	if err != nil {
		t.Fatal(err)
	}
	originalModel, err := composedModel(p, profile)
	if err != nil {
		t.Fatal(err)
	}
	mutatedModel, err := composedModel(mutant, profile)
	if err != nil {
		t.Fatal(err)
	}
	if originalModel == mutatedModel {
		t.Fatal("return change did not propagate")
	}
	found := false
	for _, n := range mutant.Nodes {
		if n.Source == "return err == nil, nil" {
			found = true
			if n.ReturnError != "false" {
				t.Fatal("nil return was not extracted")
			}
		}
	}
	if !found {
		t.Fatal("mutated return missing")
	}
}
func TestPausedProcessCancellation(t *testing.T) {
	for _, prefix := range [][]string{{"enqueue", "start", "observe", "begin"}, {"enqueue", "start", "observe", "begin", "readClaim", "readInfra"}} {
		p, err := newPausedProcess("")
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range prefix {
			if _, err = p.apply(event, false); err != nil {
				p.h.close()
				t.Fatal(err)
			}
		}
		// Prefixes leave Go blocked before an API operation (initial read or status update).
		if err = p.h.close(); err != nil {
			t.Fatal(err)
		}
	}
}

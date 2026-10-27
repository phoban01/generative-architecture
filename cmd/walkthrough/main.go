package main

import (
	"context"
	"flag"
	"fmt"
	"generative-architecture/examples/database"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/quint"
	"generative-architecture/pkg/report"
	"os"
	"path/filepath"
	"time"
)

func main() {
	refresh := flag.Bool("refresh", false, "refresh bounded Quint traces")
	flag.Parse()
	if err := run(*refresh); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(refresh bool) error {
	root := "examples/database"
	e, err := database.Load(root)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "generated")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	diagram, err := e.LikeC4()
	if err != nil {
		return err
	}
	for name, contents := range map[string]string{"Model.qnt": e.Constrained.Quint(), "architecture.c4": diagram} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(contents), 0644); err != nil {
			return err
		}
	}
	// The TransitionModel generated from controller.go, wiring.go,
	// dynamic_store.go, abstraction.yaml and process.yaml. No handwritten
	// model exists.
	if err = krm.Write(filepath.Join(dir, "model.yaml"), "TransitionModel", "database", e.Spec); err != nil {
		return err
	}
	if err = krm.WriteResult(filepath.Join(dir, "process.yaml"), "ExtractedProcess", "database-provisioning", e.Program); err != nil {
		return err
	}
	if err = krm.WriteResult(filepath.Join(dir, "watches.yaml"), "ExtractedWatches", "database", e.Watches); err != nil {
		return err
	}
	if err = krm.WriteResult(filepath.Join(dir, "dynamic-client.yaml"), "ExtractedDynamicAccess", "database", e.Dynamic); err != nil {
		return err
	}
	fmt.Println("Generated from Reconcile and SetupWithManager in Go; requirements come from constraints.yaml.")
	fmt.Println("API access: dynamic.Interface with explicit resource catalog; Database watch: unstructured with GVK.")
	fmt.Println("Database controller: see Database -> check provisioning contract -> create/find instance -> wait for provisioning -> update Database status.")
	for _, name := range []string{"absent", "unsupported", "pending", "provisioned", "repeated"} {
		trace, err := e.Schedule(name)
		if err != nil {
			return err
		}
		if err = e.CheckConstraints(trace); err != nil {
			return err
		}
		_, state, err := replay(e, trace)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s: %s; instances created=%v; Database.status.endpoint=%q\n", name, state["outcome"], state["instanceCreates"], state["Database_db1_status_endpoint"])
	}
	fmt.Println("\nAll five schedules match Go and satisfy the independent constraints.")
	if !refresh {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	options := quint.Options{Samples: 40, Steps: 60, Seed: 20261013}
	sampled, err := quint.Sample(ctx, e.Constrained, options)
	if err != nil {
		return err
	}
	traces := filepath.Join(root, "traces")
	if err = os.MkdirAll(traces, 0755); err != nil {
		return err
	}
	old, err := filepath.Glob(filepath.Join(traces, "*.itf.json"))
	if err != nil {
		return err
	}
	for _, path := range old {
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	hashes := map[string]string{}
	coverage := map[string]int{}
	steps := 0
	for i, trace := range sampled.Traces {
		if err = e.CheckConstraints(trace); err != nil {
			return err
		}
		report, _, err := replay(e, trace)
		if err != nil {
			return fmt.Errorf("Quint trace %d: %w", i, err)
		}
		steps += report.Steps
		for event, count := range report.Coverage {
			coverage[event] += count
		}
		name := fmt.Sprintf("trace-%03d.itf.json", i)
		if err = os.WriteFile(filepath.Join(traces, name), sampled.ITF[i], 0644); err != nil {
			return err
		}
		hashes[name] = goflow.Hash(sampled.ITF[i])
	}
	inputs := map[string]string{}
	// The abstraction and the adapter code generated from it decide what replay
	// observes, so they invalidate evidence like any other input (ADR-0005).
	for _, name := range []string{"controller.go", "wiring.go", "dynamic_store.go", "resources.yaml", "generate.go", "process.yaml", "constraints.yaml", "abstraction.yaml", "project.yaml", "generated/projection_generated.go", "generated/references_generated.go", "generated/environment_generated.go", "generated/gating_generated.go", "store_proxy_generated.go"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		inputs[name] = goflow.Hash(b)
	}
	v := report.Verification{
		Name:        "database-provisioning",
		Mode:        report.ModeStrict,
		Scope:       "source-derived Database controller reconcile; one Database/instance; up to four invocations; source-extracted watches/mapping and dynamic GVR operations; unstructured Database; observed dynamic client with fake API; native event handlers and FIFO; controlled/coalesced event delivery; summarized contract/store effects; no Kubernetes API errors, cache or revocation",
		Model:       "generated/Model.qnt",
		ModelSHA256: goflow.Hash([]byte(e.Constrained.Quint())),
		Sampling:    &report.Sampling{Samples: options.Samples, Steps: options.Steps, Seed: options.Seed},
		Traces:      len(sampled.Traces),
		Transitions: steps,
		Coverage:    coverage,
		TraceHashes: hashes,
		InputHashes: inputs,
	}
	v.Conclude()
	if blocking := v.Blocking(); len(blocking) > 0 {
		return fmt.Errorf("%s finding: %s", blocking[0].Category, blocking[0].Message)
	}
	if err = v.Write(filepath.Join(root, "report.yaml")); err != nil {
		return err
	}
	fmt.Printf("PASS Quint + Go replay: %d traces, %d transitions\n", len(sampled.Traces), steps)
	return nil
}
func replay(e database.Example, trace conformance.Trace) (conformance.Report, map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a := database.New(ctx, e, false)
	defer a.Close()
	report, err := conformance.ReplayObserved(ctx, e.Model, trace, a, e.Observed)
	if err != nil {
		return report, nil, err
	}
	state, err := a.Project(ctx)
	return report, state, err
}

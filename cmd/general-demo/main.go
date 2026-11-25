// general-demo checks the reusable runtime model against real controller-runtime
// queues. Business reconcilers are tested separately; these outcomes are scripted.
// It runs two profiles of the same controllers: client-go's FIFO queue and
// controller-runtime's priority queue with startup sync.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cr "github.com/phoban01/generative-architecture/pkg/controllerruntime"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"github.com/phoban01/generative-architecture/pkg/quint"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// runtimeProfile is one profile document with the model and report it
// produces, relative to the example root, and the scope its report states.
type runtimeProfile struct {
	document, modelFile, reportFile, scope string
}

var profiles = []runtimeProfile{
	{"profile.yaml", "generated/Model.qnt", "report.yaml", "bounded runtime simulation; real FIFO workers; scripted reconcile outcomes"},
	{"priority-profile.yaml", "generated/priority/Model.qnt", "priority-report.yaml", "bounded runtime simulation; real controller-runtime priority queues with startup sync; scripted reconcile outcomes"},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root := "examples/runtimegroup"
	for _, p := range profiles {
		if err := runProfile(root, p); err != nil {
			return fmt.Errorf("%s: %w", p.document, err)
		}
	}
	return nil
}

func runProfile(root string, p runtimeProfile) error {
	var profile cr.GroupProfile
	meta, err := krm.Read(filepath.Join(root, p.document), "RuntimeGroup", &profile)
	if err != nil {
		return err
	}
	generated, err := cr.BuildGroupModel(profile)
	if err != nil {
		return err
	}
	m, err := model.Compile(generated.Spec)
	if err != nil {
		return err
	}
	modelPath := filepath.Join(root, p.modelFile)
	if err = os.MkdirAll(filepath.Dir(modelPath), 0755); err != nil {
		return err
	}
	if err = os.WriteFile(modelPath, []byte(m.Quint()), 0644); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	options := quint.Options{Samples: 40, Steps: 60, Seed: 20261013}
	samples, err := quint.Sample(ctx, m, options)
	if err != nil {
		return err
	}
	steps := 0
	coverage := map[string]int{}
	for i, trace := range samples.Traces {
		report, err := cr.ReplayGroup(ctx, generated, trace)
		if err != nil {
			return fmt.Errorf("trace %d: %w", i, err)
		}
		steps += report.Steps
		for name, n := range report.Coverage {
			coverage[name] += n
		}
	}
	// Native targeted tests guarantee overlapping invocations and dirty keys;
	// sampled schedules broaden coverage without claiming exhaustiveness.
	v := report.Verification{
		Name:        meta.Name,
		Mode:        report.ModeStrict,
		Scope:       p.scope,
		Sampling:    &report.Sampling{Samples: options.Samples, Steps: options.Steps, Seed: options.Seed},
		Traces:      len(samples.Traces),
		Transitions: steps,
		Coverage:    coverage,
	}
	v.Conclude()
	if blocking := v.Blocking(); len(blocking) > 0 {
		return fmt.Errorf("%s finding: %s", blocking[0].Category, blocking[0].Message)
	}
	if err = v.Write(filepath.Join(root, p.reportFile)); err != nil {
		return err
	}
	fmt.Printf("PASS reusable runtime (%s): %d traces, %d transitions\n", meta.Name, len(samples.Traces), steps)
	return nil
}

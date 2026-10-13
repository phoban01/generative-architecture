// library-demo exercises an unrelated consumer of the public library.
package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"generative-architecture/examples/configmirror"
	"generative-architecture/pkg/conformance"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/quint"
	"os"
	"path/filepath"
	"time"
)

func main() {
	root := flag.String("root", "examples/configmirror", "example directory")
	refresh := flag.Bool("refresh", false, "refresh traces using Quint")
	generateOnly := flag.Bool("generate-only", false, "generate model and diagram without replay")
	flag.Parse()
	if flag.NArg() != 0 || (*refresh && *generateOnly) {
		fmt.Fprintln(os.Stderr, "unexpected arguments or incompatible flags")
		os.Exit(2)
	}
	if err := run(*root, *refresh, *generateOnly); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(root string, refresh, generateOnly bool) error {
	reportPath := filepath.Join(root, "report.yaml")
	if err := os.Remove(reportPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	m, err := configmirror.Load(filepath.Join(root, "system.yaml"))
	if err != nil {
		return err
	}
	generated := filepath.Join(root, "generated")
	if err = os.MkdirAll(generated, 0755); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(generated, "Model.qnt"), []byte(m.Quint()), 0644); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(generated, "architecture.c4"), []byte(m.LikeC4()), 0644); err != nil {
		return err
	}
	if generateOnly {
		fmt.Println("Generated ConfigMirror model and diagram")
		return nil
	}
	if refresh {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		result, err := quint.Sample(ctx, m, quint.Options{Samples: 40, Steps: 60, Seed: 20261013})
		if err != nil {
			return err
		}
		dir := filepath.Join(root, "traces")
		if err = os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		old, err := filepath.Glob(filepath.Join(dir, "*.itf.json"))
		if err != nil {
			return err
		}
		for _, p := range old {
			if err = os.Remove(p); err != nil {
				return err
			}
		}
		for i, b := range result.ITF {
			if err = os.WriteFile(filepath.Join(dir, fmt.Sprintf("trace-%03d.itf.json", i)), b, 0644); err != nil {
				return err
			}
		}
	}
	paths, err := filepath.Glob(filepath.Join(root, "traces/*.itf.json"))
	if err != nil {
		return err
	}
	if len(paths) != 40 {
		return fmt.Errorf("expected 40 traces; use --refresh")
	}
	total := 0
	coverage := map[string]int{}
	hashes := map[string]string{}
	for _, p := range paths {
		trace, err := conformance.ReadITF(p)
		if err != nil {
			return err
		}
		report, err := func() (conformance.Report, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, err := configmirror.New(ctx, m.Effects())
			if err != nil {
				return conformance.Report{}, err
			}
			r, err := conformance.Replay(ctx, m, trace, a)
			closed := a.Close()
			if err == nil {
				err = closed
			}
			return r, err
		}()
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		total += report.Steps
		for event, n := range report.Coverage {
			coverage[event] += n
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		hashes[filepath.Base(p)] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	report := map[string]any{"result": "passed", "traces": len(paths), "transitions": total, "coverage": coverage, "traceHashes": hashes, "modelSha256": fmt.Sprintf("%x", sha256.Sum256([]byte(m.Quint()))), "scope": "bounded simulation; real worker/FIFO; fake API; controlled cache; declared behavior"}
	if err = krm.WriteResult(reportPath, "VerificationReport", "config-mirror", report); err != nil {
		return err
	}
	fmt.Printf("PASS config mirror: %d traces, %d transitions\n", len(paths), total)
	return nil
}

package runtimecheck

import (
	"crypto/sha256"
	"fmt"
	"generative-architecture/pkg/krm"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func sourceHashes(root string) (map[string]string, error) {
	out := map[string]string{}
	add := func(path string) error {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return err
		}
		out[filepath.ToSlash(path)] = digest(b)
		return nil
	}
	for _, p := range []string{"go.mod", "go.sum", "system.yaml"} {
		if err := add(p); err != nil {
			return nil, err
		}
	}
	for _, dir := range []string{"pkg", "examples/machinevendor", "cmd/runtime-demo"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				rel, e := filepath.Rel(root, path)
				if e != nil {
					return e
				}
				return add(rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// VerifyEvidence rejects changed models, profiles, implementation sources and traces.
func VerifyEvidence(root string) error {
	var report map[string]any
	if err := krm.ReadResult(filepath.Join(root, "reports/runtime/summary.yaml"), "VerificationReport", &report); err != nil {
		return err
	}
	p, err := Load(root)
	if err != nil {
		return err
	}
	generated, err := os.ReadFile(filepath.Join(root, "generated/runtime/Runtime.qnt"))
	if err != nil {
		return err
	}
	if report["modelSha256"] != digest(generated) || report["modelSha256"] != digest([]byte(render(p))) {
		return fmt.Errorf("runtime evidence model drift")
	}
	path, err := profilePath(root)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if report["profileSha256"] != digest(b) {
		return fmt.Errorf("runtime evidence profile drift")
	}
	sources, err := sourceHashes(root)
	if err != nil {
		return err
	}
	stored, ok := report["sourceHashes"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing runtime source receipt")
	}
	current := map[string]any{}
	for k, v := range sources {
		current[k] = v
	}
	if !reflect.DeepEqual(stored, current) {
		return fmt.Errorf("runtime implementation changed; rerun runtime-demo")
	}
	traces, ok := report["traceHashes"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing trace hashes")
	}
	files, err := filepath.Glob(filepath.Join(root, "reports/runtime/trace-*.itf.json"))
	if err != nil {
		return err
	}
	if len(files) != 105 || len(traces) != len(files) {
		return fmt.Errorf("runtime trace inventory changed")
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if traces[filepath.Base(path)] != digest(b) {
			return fmt.Errorf("runtime trace changed: %s", path)
		}
	}
	return nil
}

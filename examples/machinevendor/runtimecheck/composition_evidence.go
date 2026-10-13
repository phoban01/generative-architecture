package runtimecheck

import (
	"fmt"
	"generative-architecture/examples/machinevendor/architecture"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/sourcegen"
	"os"
	"path/filepath"
	"reflect"
)

func compositionHashes(root string) (map[string]string, error) {
	hashes, err := sourceHashes(root)
	if err != nil {
		return nil, err
	}
	var sys architecture.System
	if _, err = krm.Read(filepath.Join(root, "system.yaml"), "System", &sys); err != nil {
		return nil, err
	}
	cfg, _, _, err := loadComposition(root)
	if err != nil {
		return nil, err
	}
	files := []string{sys.Composition, sys.Runtime, cfg.Process, "cmd/composition-demo/main.go", "contracts.lock.yaml"}
	if sys.Sources != nil {
		files = append(files, sys.Sources.Mappings, sys.Sources.Descriptor)
	}
	for _, component := range sys.Components {
		files = append(files, component.Contract)
	}
	for _, rel := range files {
		path, err := sourcegen.LocalPath(root, rel)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		hashes[rel] = digest(b)
	}
	return hashes, nil
}
func VerifyCompositionEvidence(root string) error {
	var report map[string]any
	if err := krm.ReadResult(filepath.Join(root, "reports/composition/summary.yaml"), "VerificationReport", &report); err != nil {
		return err
	}
	_, p, profile, err := loadComposition(root)
	if err != nil {
		return err
	}
	q, err := composedModel(p, profile)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(root, "generated/composition/Composed.qnt"))
	if err != nil {
		return err
	}
	if report["modelSha256"] != digest(b) || report["modelSha256"] != digest([]byte(q)) {
		return fmt.Errorf("composed model drift; rerun composition-demo")
	}
	hashes, err := compositionHashes(root)
	if err != nil {
		return err
	}
	current := map[string]any{}
	for k, v := range hashes {
		current[k] = v
	}
	if !reflect.DeepEqual(current, report["inputHashes"]) {
		return fmt.Errorf("composition inputs changed; rerun composition-demo")
	}
	traces, ok := report["traceHashes"].(map[string]any)
	if !ok || len(traces) != 110 {
		return fmt.Errorf("composition evidence incomplete")
	}
	paths, err := filepath.Glob(filepath.Join(root, "reports/composition/*.itf.json"))
	if err != nil {
		return err
	}
	if len(paths) != len(traces) {
		return fmt.Errorf("composition trace inventory changed")
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if traces[filepath.Base(path)] != digest(b) {
			return fmt.Errorf("composition trace changed: %s", path)
		}
	}
	return nil
}

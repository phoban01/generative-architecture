package contracts

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestExternalSelectorInModuleCache checks unannotated k8s.io/api types,
// which resolve from the module cache through go.mod of this repository.
func TestExternalSelectorInModuleCache(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	reference := Definition{Name: "object-reference", Version: "v1", Fields: map[string]string{"name": "string", "namespace": "string"}}
	schedulable := Definition{Name: "schedulable", Version: "v1", Fields: map[string]string{"unschedulable": "string"}}
	for _, tc := range []struct {
		name    string
		impl    Implementation
		wantErr string
	}{
		{name: "compatible type", impl: Implementation{Contract: "object-reference/v1", Package: "k8s.io/api/core/v1", Type: "ObjectReference"}},
		{name: "field type drift", impl: Implementation{Contract: "schedulable/v1", Package: "k8s.io/api/core/v1", Type: "NodeSpec"}, wantErr: "external selector k8s.io/api/core/v1.NodeSpec for schedulable/v1: field unschedulable: expected string, got bool"},
		{name: "missing field", impl: Implementation{Contract: "object-reference/v1", Package: "k8s.io/api/core/v1", Type: "ObjectReference", Fields: map[string]string{"name": "nodeName"}}, wantErr: "field name: missing contract field nodeName"},
		{name: "missing type", impl: Implementation{Contract: "object-reference/v1", Package: "k8s.io/api/core/v1", Type: "NoSuchType"}, wantErr: "type NoSuchType not found in k8s.io/api/core/v1"},
		{name: "missing module", impl: Implementation{Contract: "object-reference/v1", Package: "example.invalid/absent/v1", Type: "Thing"}, wantErr: "external selector package example.invalid/absent/v1: not resolvable from the module graph"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence, err := Inspect(root, []Definition{reference, schedulable}, nil, []Implementation{tc.impl})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got error %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(evidence) != 1 {
				t.Fatalf("got %d evidence records, want 1", len(evidence))
			}
			e := evidence[0]
			if e.Origin != "external-selector" || e.Module != "k8s.io/api" || e.Version == "" || e.GoType != "k8s.io/api/core/v1.ObjectReference" {
				t.Fatalf("unexpected evidence %s %s %s@%s", e.Origin, e.GoType, e.Module, e.Version)
			}
			if e.JSONPaths["name"] != "name" || e.JSONPaths["namespace"] != "namespace" {
				t.Fatalf("unexpected JSON paths %v", e.JSONPaths)
			}
			// Source hashes name module@version paths, not module cache paths.
			key := "k8s.io/api@" + e.Version + "/core/v1/types.go"
			if e.SourceHashes[key] == "" || e.SourceHashes["type-shape"] == "" {
				t.Fatalf("missing source hash %s or type-shape", key)
			}
			for k := range e.SourceHashes {
				if strings.HasPrefix(k, "..") || filepath.IsAbs(k) {
					t.Fatalf("source hash key %s depends on the module cache location", k)
				}
			}
		})
	}
}

package krm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceBoundary(t *testing.T) {
	good := "apiVersion: architecture.local/v1alpha1\nkind: Example\nmetadata:\n  name: example\nspec:\n  expression: x == 1\n"
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"valid", good, true},
		{"wrong kind", strings.Replace(good, "kind: Example", "kind: Other", 1), false},
		{"wrong version", strings.Replace(good, "v1alpha1", "v99", 1), false},
		{"unknown spec field", good + "  typo: true\n", false},
		{"duplicate", good + "  expression: y\n", false},
		{"multiple documents", good + "---\n" + good, false},
		{"no spec", strings.Replace(good, "spec:\n  expression: x == 1\n", "", 1), false},
		{"invalid name", strings.Replace(good, "name: example", "name: Bad Name", 1), false},
		{"namespace", strings.Replace(good, "name: example", "name: example\n  namespace: silently-ignored", 1), false},
		{"status as input", strings.Replace(good, "spec:", "status:", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "x.yaml")
			if err := os.WriteFile(p, []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			var spec struct {
				Expression string `yaml:"expression"`
			}
			_, err := Read(p, "Example", &spec)
			if (err == nil) != tc.ok {
				t.Fatalf("got %v", err)
			}
		})
	}
}

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

func TestLoadReferenceSystem(t *testing.T) {
	p, err := Load("../../examples/refsystem/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "refsystem" || p.Spec.Module != "github.com/phoban01/generative-architecture" || len(p.Spec.Packages) != 1 || p.Spec.Runtime.Profile != "controller-runtime" {
		t.Fatalf("unexpected document %+v", p.Spec)
	}
	if p.Spec.Bounds["maxInvocations"] != 4 {
		t.Fatalf("bounds %v", p.Spec.Bounds)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != root {
		t.Fatalf("module root %s, want %s", p.Root, root)
	}
	want := []string{"config/crd/refsystem.example.org_backups.yaml", "config/crd/refsystem.example.org_databases.yaml", "config/crd/refsystem.example.org_tenants.yaml", "project.yaml"}
	got := p.SortedHashes()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hashed %v, want %v", got, want)
	}
	for _, name := range got {
		if len(p.Hashes[name]) != 64 {
			t.Fatalf("%s: hash %q", name, p.Hashes[name])
		}
	}
	c, err := p.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries()) != 3 {
		t.Fatalf("catalog %v", c.Entries())
	}
	if e, ok := c.Lookup(ir.GVK{Group: "refsystem.example.org", Version: "v1alpha1", Kind: "Backup"}); !ok || e.GVR.Resource != "backups" {
		t.Fatalf("Backup entry %+v %v", e, ok)
	}
}

const good = `apiVersion: architecture.local/v1alpha1
kind: Project
metadata:
  name: sample
spec:
  module: example.org/sample
  packages: [./controllers]
  catalog:
    discovery: [discovery.json]
  constraints: [constraints.yaml]
  bounds: {maxInvocations: 2}
`

func writeSample(t *testing.T, doc string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module example.org/sample\n\ngo 1.25\n", "discovery.json": "[]", "constraints.yaml": "unused: true\n", "project.yaml": doc} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "controllers"), 0755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "project.yaml")
}

func TestLoadResolvesRelativeToDocumentAndModule(t *testing.T) {
	path := writeSample(t, good)
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != filepath.Dir(path) || p.Dir != filepath.Dir(path) {
		t.Fatalf("root %s dir %s", p.Root, p.Dir)
	}
	if len(p.Hashes) != 3 || p.Hashes["discovery.json"] == "" {
		t.Fatalf("hashes %v", p.Hashes)
	}
	if got := p.Paths(); len(got) != 2 || got[1] != filepath.Join(p.Dir, "constraints.yaml") {
		t.Fatalf("paths %v", got)
	}
}

func TestLoadFailures(t *testing.T) {
	cases := map[string]struct {
		doc, want string
	}{
		"missing package":    {strings.Replace(good, "./controllers", "./missing", 1), "missing"},
		"absolute package":   {strings.Replace(good, "./controllers", "/controllers", 1), "relative pattern"},
		"escaping package":   {strings.Replace(good, "./controllers", "./../x", 1), "relative pattern"},
		"missing document":   {strings.Replace(good, "discovery.json", "absent.json", 1), "absent.json"},
		"escaping document":  {strings.Replace(good, "constraints.yaml", "../constraints.yaml", 1), "inside its directory"},
		"wrong module":       {strings.Replace(good, "example.org/sample", "example.org/other", 1), "declares example.org/sample"},
		"invalid module":     {strings.Replace(good, "example.org/sample", "not a module", 1), "invalid module"},
		"no packages":        {strings.Replace(good, "packages: [./controllers]", "packages: []", 1), "at least one package"},
		"duplicate package":  {strings.Replace(good, "packages: [./controllers]", "packages: [./controllers, ./controllers]", 1), "twice"},
		"non-positive bound": {strings.Replace(good, "maxInvocations: 2", "maxInvocations: 0", 1), "positive"},
		"bad bound name":     {strings.Replace(good, "maxInvocations: 2", "max-invocations: 2", 1), "identifier"},
		"unknown field":      {good + "  extra: true\n", "extra"},
		"wrong kind":         {strings.Replace(good, "kind: Project", "kind: Abstraction", 1), "expected"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeSample(t, tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

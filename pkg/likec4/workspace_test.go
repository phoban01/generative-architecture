package likec4

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"generative-architecture/pkg/ir"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// golden renders the workspace as one text, each file headed by its path,
// and compares it with testdata/<name>.golden.
func golden(t *testing.T, name string, w Workspace) {
	t.Helper()
	files, err := w.Files()
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	for _, path := range sortedKeys(files) {
		b.WriteString("-- " + filepath.ToSlash(path) + " --\n")
		b.Write(files[path])
	}
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run go test ./pkg/likec4 -update", err)
	}
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("%s differs; run go test ./pkg/likec4 -update and review the diff\n%s", path, b.String())
	}
	// The same workspace renders the same bytes every time.
	again, err := w.Files()
	if err != nil {
		t.Fatal(err)
	}
	for p, content := range files {
		if !bytes.Equal(again[p], content) {
			t.Fatalf("%s is not deterministic", p)
		}
	}
}

func twoElements() Workspace {
	var w Workspace
	elements := []Element{
		{ID: "mirror", Kind: KindController, Title: "Mirror controller", Description: "copies a ConfigMap", Evidence: ir.Extracted, Source: ir.Source{File: "controller.go", Line: 12}},
		{ID: "configmap", Kind: KindResource, Title: "v1/ConfigMap", Evidence: ir.Declared},
	}
	relations := []Relation{{From: "mirror", To: "configmap", Label: "writes", Description: "update (direct)", Evidence: ir.Extracted, Source: ir.Source{File: "controller.go", Line: 30}}}
	if err := w.Add(elements, relations, View{Name: "overview", Title: "Overview", Include: []string{"mirror", "configmap"}}); err != nil {
		panic(err)
	}
	return w
}

func TestWorkspaceGolden(t *testing.T) {
	golden(t, "workspace", twoElements())
}

func TestWriteReplacesStaleViews(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "views"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "views", "old.c4"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := twoElements().Write(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"spec.c4", "model.c4", "views/overview.c4"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "views", "old.c4")); !os.IsNotExist(err) {
		t.Fatalf("stale view kept: %v", err)
	}
}

func TestID(t *testing.T) {
	for in, want := range map[string]string{
		"mirror":                          "mirror",
		"apps/v1/Deployment":              "apps_v1_Deployment",
		"demo.architecture.local/v1/Kind": "demo_architecture_local_v1_Kind",
		"1st":                             "e_1st",
		"":                                "e_",
		"_x":                              "e__x",
	} {
		if got := ID(in); got != want {
			t.Errorf("ID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	base := twoElements()
	for name, mutate := range map[string]func(*Workspace){
		"no evidence":       func(w *Workspace) { w.Elements[0].Evidence = "" },
		"unknown kind":      func(w *Workspace) { w.Elements[0].Kind = "database" },
		"bad identifier":    func(w *Workspace) { w.Elements[0].ID = "a/b" },
		"dangling relation": func(w *Workspace) { w.Relations[0].To = "missing" },
		"relation evidence": func(w *Workspace) { w.Relations[0].Evidence = "guessed" },
		"unknown include":   func(w *Workspace) { w.Views[0].Include = []string{"missing"} },
		"duplicate view":    func(w *Workspace) { w.Views = append(w.Views, w.Views[0]) },
		"duplicate element": func(w *Workspace) { w.Elements = append(w.Elements, w.Elements[0]) },
	} {
		w := Workspace{Elements: append([]Element(nil), base.Elements...), Relations: append([]Relation(nil), base.Relations...), Views: append([]View(nil), base.Views...)}
		mutate(&w)
		if err := w.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	var w Workspace
	if err := w.Add(base.Elements, nil, View{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	changed := base.Elements[0]
	changed.Title = "other"
	if err := w.Add([]Element{changed}, nil, View{Name: "b"}); err == nil {
		t.Fatal("conflicting element accepted")
	}
}

// likec4Validate runs `likec4 validate` on the written workspace when
// LIKEC4_BIN is set.
func likec4Validate(t *testing.T, w Workspace) {
	t.Helper()
	bin := os.Getenv("LIKEC4_BIN")
	if bin == "" {
		t.Skip("LIKEC4_BIN not set; skipping LikeC4 validation")
	}
	dir := t.TempDir()
	if err := w.Write(dir); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "validate", dir).CombinedOutput()
	if err != nil || strings.Contains(strings.ToLower(string(out)), "error") {
		t.Fatalf("likec4 validate: %v\n%s", err, out)
	}
}

func TestLikeC4ParsesWorkspace(t *testing.T) {
	likec4Validate(t, twoElements())
}

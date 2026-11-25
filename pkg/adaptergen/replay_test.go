package adaptergen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"golang.org/x/tools/go/packages"
)

// typecheckPackage typechecks several generated files as one package of
// this module, in an overlay.
func typecheckPackage(t *testing.T, files map[string][]byte) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const virtual = "pkg/adaptergen/internal/replaycheck"
	overlay := map[string][]byte{}
	for name, src := range files {
		overlay[filepath.Join(root, filepath.FromSlash(virtual), name)] = src
	}
	cfg := &packages.Config{
		Dir:     root,
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Overlay: overlay,
	}
	pkgs, err := packages.Load(cfg, "./"+virtual)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected one package, got %d", len(pkgs))
	}
	for _, e := range pkgs[0].Errors {
		t.Errorf("generated source: %v", e)
	}
	if t.Failed() {
		t.Fatalf("generated program does not typecheck:\n%s", files["main_generated.go"])
	}
}

// configMirrorReplay describes ConfigMirror's replay: two ConfigMaps, the
// source read through the cache, and a reconciler with a cached client, a
// direct reader and the target key.
func configMirrorReplay() adaptergen.ReplayMain {
	configMap := ir.GVK{Version: "v1", Kind: "ConfigMap"}
	r := adaptergen.ReplayMain{
		Types: []adaptergen.ReplayType{{Import: "k8s.io/api/core/v1", Name: "ConfigMap", Resource: configMap}},
		Objects: []adaptergen.ReplayObject{
			{Resource: configMap, Namespace: "demo", Name: "source", Fields: []adaptergen.ReplayValue{{Path: []string{"data", "value"}, Type: "string", Literal: `"a"`}}, Cached: true},
			{Resource: configMap, Namespace: "demo", Name: "copy", Fields: []adaptergen.ReplayValue{{Path: []string{"data", "value"}, Type: "string", Literal: `""`}}},
		},
		Key:        ir.ObjectRef{Namespace: "demo", Name: "source"},
		RetryTicks: 2,
	}
	r.Reconciler.Import = "github.com/phoban01/generative-architecture/examples/configmirror"
	r.Reconciler.Name = "Reconciler"
	r.Reconciler.Fields = []adaptergen.ReplayField{
		{Name: "Client", Kind: adaptergen.ReplayClient},
		{Name: "APIReader", Kind: adaptergen.ReplayReader},
		{Name: "Target", Kind: adaptergen.ReplayKey, Key: ir.ObjectRef{Namespace: "demo", Name: "copy"}},
	}
	return r
}

// The replay program typechecks beside ConfigMirror's generated
// projection, environment, gating and runtime files, renamed to package
// main, and wires the reconciler as the hand-written adapter does.
func TestGenerateReplayMain(t *testing.T) {
	src, err := adaptergen.GenerateReplayMain("main", configMirrorReplay())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`observer.Client(cr.SplitClient(api, cache), "cached")`,
		`observer.Reader(api, "direct")`,
		`client.ObjectKey{Namespace: "demo", Name: "copy"}`,
		`scheme.AddKnownTypes(gv, &p1.ConfigMap{})`,
		`unstructured.SetNestedField(obj.Object, "a", "data", "value")`,
		`cache = append(cache, obj.DeepCopy())`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("generated program lacks %q", want)
		}
	}
	files := map[string][]byte{"main_generated.go": src}
	for _, name := range []string{"projection", "environment", "gating", "runtime"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "examples", "configmirror", "generated", name+"_generated.go"))
		if err != nil {
			t.Fatal(err)
		}
		files[name+"_generated.go"] = bytes.Replace(b, []byte("package generated"), []byte("package main"), 1)
	}
	typecheckPackage(t, files)
}

// Without a cached object the program builds one API client and wires the
// manager's client to it directly.
func TestGenerateReplayMainWithoutCache(t *testing.T) {
	r := configMirrorReplay()
	r.Objects[0].Cached = false
	src, err := adaptergen.GenerateReplayMain("main", r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "cache client.Client") || !strings.Contains(string(src), `observer.Client(api, "direct")`) {
		t.Fatalf("program without a cache:\n%s", src)
	}
}

func TestGenerateReplayMainRejects(t *testing.T) {
	for name, edit := range map[string]func(*adaptergen.ReplayMain){
		"unknown field kind":  func(r *adaptergen.ReplayMain) { r.Reconciler.Fields[0].Kind = "manager" },
		"unregistered object": func(r *adaptergen.ReplayMain) { r.Objects[0].Resource.Kind = "Secret" },
		"key without object":  func(r *adaptergen.ReplayMain) { r.Reconciler.Fields[2].Key = ir.ObjectRef{} },
		"missing key":         func(r *adaptergen.ReplayMain) { r.Key = ir.ObjectRef{} },
	} {
		t.Run(name, func(t *testing.T) {
			r := configMirrorReplay()
			edit(&r)
			if _, err := adaptergen.GenerateReplayMain("main", r); err == nil {
				t.Fatal("generated a program from an invalid description")
			}
		})
	}
}

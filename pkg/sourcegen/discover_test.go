package sourcegen

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// stubRoot writes a module that stands in for controller-runtime: discovery
// looks interfaces and parameter types up by import path, so a stub with the
// same paths exercises the same code as the real dependency, offline.
func stubRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{
		"go.mod":                   "module sigs.k8s.io/controller-runtime\n\ngo 1.25.0\n",
		"go.sum":                   "",
		"alias.go":                 "package controllerruntime\nimport \"sigs.k8s.io/controller-runtime/pkg/manager\"\ntype Manager = manager.Manager\n",
		"pkg/manager/manager.go":   "package manager\ntype Manager interface{ Start() error }\n",
		"pkg/reconcile/types.go":   "package reconcile\nimport \"context\"\ntype Request struct{}\ntype Result struct{}\ntype Reconciler interface{ Reconcile(context.Context, Request) (Result, error) }\n",
		"pkg/client/interfaces.go": "package client\ntype Reader interface{ Get() error }\ntype Client interface{ Reader; Update() error }\n",
	}
	for path, body := range files {
		all[path] = body
	}
	for path, body := range all {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const stubController = `package demo
import (
 "context"
 ctrl "sigs.k8s.io/controller-runtime"
 "sigs.k8s.io/controller-runtime/pkg/client"
 "sigs.k8s.io/controller-runtime/pkg/reconcile"
)
// Reconciler reconciles things.
type Reconciler struct{ Client client.Client }
func (r *Reconciler) Reconcile(context.Context, reconcile.Request) (reconcile.Result, error) { return reconcile.Result{}, nil }
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error { return nil }
// Helper is a plain struct that happens to live beside the reconciler.
type Helper struct{}
func (Helper) Reconcile(context.Context) error { return nil }
`

func TestDiscoverControllersFromExamplesWithoutMarkers(t *testing.T) {
	controllers, err := DiscoverControllers("../..", []string{"./examples/configmirror", "./examples/database"})
	if err != nil {
		t.Fatal(err)
	}
	if len(controllers) != 2 {
		t.Fatalf("expected the two example reconcilers, got %+v", controllers)
	}
	mirror, database := controllers[0], controllers[1]
	if mirror.ID != "generative-architecture/examples/configmirror.Reconciler" || mirror.Name != "Reconciler" || mirror.Package != "generative-architecture/examples/configmirror" || mirror.Receiver != "Reconciler" {
		t.Fatalf("configmirror reconciler not discovered: %+v", mirror)
	}
	if mirror.Setup != nil {
		t.Fatalf("configmirror has no setup method: %+v", mirror.Setup)
	}
	if mirror.Provenance.Source.File != "examples/configmirror/controller.go" || mirror.Provenance.Source.Line == 0 || mirror.Provenance.Module != "generative-architecture" {
		t.Fatalf("wrong provenance: %+v", mirror.Provenance)
	}
	b, err := os.ReadFile("../../examples/configmirror/controller.go")
	if err != nil {
		t.Fatal(err)
	}
	if mirror.Provenance.Source.SHA256 != Hash(b) {
		t.Fatal("provenance hash does not cover the declaring file")
	}
	if err := mirror.Provenance.Validate(); err != nil {
		t.Fatal(err)
	}
	if database.ID != "generative-architecture/examples/database.DatabaseReconciler" || database.Name != "DatabaseReconciler" {
		t.Fatalf("database reconciler not discovered: %+v", database)
	}
	if database.Setup == nil || database.Setup.File != "examples/database/wiring.go" || database.Setup.Line != lineOf(t, "../../examples/database/wiring.go", "SetupWithManager(mgr ctrl.Manager)") || database.Setup.SHA256 == "" {
		t.Fatalf("setup location not recorded: %+v", database.Setup)
	}
	again, err := DiscoverControllers("../..", []string{"./examples/configmirror", "./examples/database"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(controllers, again) {
		t.Fatal("nondeterministic discovery")
	}
}

func TestDiscoverControllersMarkerOnlyOverridesName(t *testing.T) {
	root := stubRoot(t, map[string]string{"demo/controller.go": strings.Replace(stubController, "// Reconciler reconciles things.", "// +arch:controller=mirror", 1)})
	controllers, err := DiscoverControllers(root, []string{"./demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(controllers) != 1 || controllers[0].Name != "mirror" || controllers[0].Receiver != "Reconciler" || controllers[0].ID != "sigs.k8s.io/controller-runtime/demo.Reconciler" {
		t.Fatalf("marker did not override only the name: %+v", controllers)
	}
	if controllers[0].Setup == nil || controllers[0].Setup.File != "demo/controller.go" || controllers[0].Setup.Line != 11 {
		t.Fatalf("setup not located: %+v", controllers[0].Setup)
	}
	plain, err := DiscoverControllers(root, []string{"./pkg/reconcile"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 0 {
		t.Fatalf("interface package has no reconciler structs: %+v", plain)
	}
}

func TestDiscoverControllersFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"same name in two packages", map[string]string{"a/controller.go": stubController, "b/controller.go": stubController}, "share the name"},
		{"setup with another signature", map[string]string{"demo/controller.go": strings.Replace(stubController, "SetupWithManager(mgr ctrl.Manager) error", "SetupWithManager(mgr ctrl.Manager, name string) error", 1)}, "exactly one controller-runtime Manager"},
		{"setup promoted from an embedded type", map[string]string{"demo/controller.go": strings.Replace(strings.Replace(stubController, "func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error { return nil }\n", "", 1), "type Reconciler struct{ Client client.Client }", "type Base struct{}\nfunc (Base) SetupWithManager(mgr ctrl.Manager) error { return nil }\ntype Reconciler struct{ Base; Client client.Client }", 1)}, "not promoted"},
		{"empty marker", map[string]string{"demo/controller.go": strings.Replace(stubController, "// Reconciler reconciles things.", "// +arch:controller=", 1)}, "missing argument"},
		{"non-local pattern", nil, "explicit local package paths"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := tc.files
			patterns := []string{"./..."}
			if tc.files == nil {
				files = map[string]string{"demo/controller.go": stubController}
				patterns = []string{"demo"}
			}
			_, err := DiscoverControllers(stubRoot(t, files), patterns)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// lineOf returns the 1-based line of the first line of path containing text.
func lineOf(t *testing.T, path, text string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, text) {
			return i + 1
		}
	}
	t.Fatalf("%s not found in %s", text, path)
	return 0
}

package sourcegen

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This self-contained fixture supplies only the reconcile signature's named
// types; production extraction separately type-checks the actual demo package.
const fixture = `// +groupName=demo.local
// +versionName=v1
package demo
import (
 "context"
 "sigs.k8s.io/controller-runtime/pkg/reconcile"
)
// +arch:resource
type Database struct { Spec Spec ` + "`json:\"spec\"`" + ` }
type Spec struct { Wanted bool ` + "`json:\"wanted\"`" + ` }
// +arch:resource
type DatabaseInstance struct { Status string ` + "`json:\"status\"`" + ` }
// +arch:controller=database
type DatabaseReconciler struct{}
func (*DatabaseReconciler) Reconcile(context.Context,reconcile.Request)(reconcile.Result,error){return reconcile.Result{},nil}
// +arch:action=publish
func (*DatabaseReconciler) Publish() bool { return true }
`

func fixtureRoot(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{
		"go.mod": "module sigs.k8s.io/controller-runtime\n\ngo 1.25.0\n", "go.sum": "",
		"pkg/reconcile/types.go": "package reconcile\ntype Request struct{}\ntype Result struct{}\n",
		"types.go":               src,
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "source-mappings.yaml"), []byte("apiVersion: architecture.local/v1alpha1\nkind: SourceBindings\nmetadata:\n  name: database\nspec:\n  controllers:\n    database:\n      primary: Database\n      owns: DatabaseInstance\n      ports:\n        eligible: Database.Spec.Wanted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestExtractTypesMarkersAndDeterminism(t *testing.T) {
	root := fixtureRoot(t, fixture)
	c := Config{Packages: []string{"./"}, Descriptor: "generated/sources.yaml", Mappings: "source-mappings.yaml"}
	d, err := Extract(root, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Resources) != 2 || len(d.Controllers) != 1 {
		t.Fatalf("missing mappings: %+v", d)
	}
	port := d.Controllers[0].Ports[0]
	if port.JSONPath != "spec.wanted" || port.Type != "map(string, bool)" || port.GoType != "bool" {
		t.Fatalf("wrong inferred field: %+v", port)
	}
	if d.Controllers[0].Actions[0].Method != "Publish" {
		t.Fatal("method not extracted")
	}
	again, err := Extract(root, c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d, again) {
		t.Fatal("nondeterministic extraction")
	}
	if err := d.Verify(root, c); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.go"), []byte("package demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := d.Verify(root, c); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("new source file not detected: %v", err)
	}
}
func TestExtractionRejectsBrokenDeclarations(t *testing.T) {
	for _, tc := range []struct{ name, old, new, want string }{
		{"unknown marker", "+arch:controller=database", "+arch:controllr=database", "unknown architecture marker"},
		{"orphan action", "// +arch:controller=database", "// ordinary comment", "action marker on a type"},
		{"unsupported field", "Wanted bool", "Wanted []string", "unsupported model value"},
		{"bad reconcile", "Reconcile(context.Context,reconcile.Request)", "Reconcile(context.Context)", "must implement Reconcile"},
		{"missing group", "+groupName=demo.local", "ordinary comment", "needs a package-level"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixtureRoot(t, strings.Replace(fixture, tc.old, tc.new, 1))
			_, err := Extract(root, Config{Packages: []string{"./"}, Mappings: "source-mappings.yaml"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
func TestFieldMappingAndTypeDrift(t *testing.T) {
	for _, tc := range []struct{ selector, want string }{
		{"Database.Spec.Missing", "does not exist"},
		{"Unknown.Spec.Wanted", "unknown port resource"},
	} {
		t.Run(tc.selector, func(t *testing.T) {
			root := fixtureRoot(t, fixture)
			path := filepath.Join(root, "source-mappings.yaml")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(b), "Database.Spec.Wanted", tc.selector, 1)), 0644); err != nil {
				t.Fatal(err)
			}
			_, err = Extract(root, Config{Packages: []string{"./"}, Mappings: "source-mappings.yaml"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	root := fixtureRoot(t, strings.Replace(fixture, "Wanted bool", "Wanted string", 1))
	d, err := Extract(root, Config{Packages: []string{"./"}, Mappings: "source-mappings.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Controllers[0].Ports[0].Type != "map(string, string)" {
		t.Fatal("Go type change did not propagate")
	}
	if d.Resources[0].Group != "demo.local" || d.Resources[0].Version != "v1" {
		t.Fatal("package metadata not used")
	}
}

func TestExtractArbitraryNamedRecord(t *testing.T) {
	source := strings.Replace(fixture, "Wanted bool", "Wanted Reading", 1) + "\ntype Reading struct { Degrees int64 `json:\"degrees\"`; Valid bool `json:\"valid\"` }\n"
	root := fixtureRoot(t, source)
	descriptor, err := Extract(root, Config{Packages: []string{"./"}, Mappings: "source-mappings.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(descriptor.Records, map[string]map[string]string{"Reading": {"degrees": "int", "valid": "bool"}}) {
		t.Fatalf("record shape missing: %v", descriptor.Records)
	}
	if descriptor.Controllers[0].Ports[0].Type != "map(string, Reading)" {
		t.Fatalf("wrong record binding: %+v", descriptor.Controllers[0].Ports)
	}
}

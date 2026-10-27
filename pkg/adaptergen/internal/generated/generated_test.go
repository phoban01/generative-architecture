package generated

import (
	"context"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/fixture"
	"github.com/phoban01/generative-architecture/pkg/ir"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var update = flag.Bool("update", false, "regenerate the committed generated files")

// assertGenerated keeps a committed file equal to the generator output so the
// runtime tests below exercise exactly what the generator emits.
func assertGenerated(t *testing.T, file string, src []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(file, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	have, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(have) != string(src) {
		t.Fatalf("%s is stale; run go test ./pkg/adaptergen/internal/generated -update", file)
	}
}

func newAPI(t *testing.T) (dynamic.Interface, meta.RESTMapper) {
	t.Helper()
	c, err := fixture.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	// No provider types are registered: objects are stored and read unstructured.
	return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), c.RESTMapper()
}

func create(t *testing.T, api dynamic.Interface, mapper meta.RESTMapper, gvk ir.GVK, namespace, name string, fields map[string]any) {
	t.Helper()
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk.Schema())
	obj.SetNamespace(namespace)
	obj.SetName(name)
	for path, value := range fields {
		if err := unstructured.SetNestedField(obj.Object, value, strings.Split(path, ".")...); err != nil {
			t.Fatal(err)
		}
	}
	mapping, err := mapper.RESTMapping(gvk.Schema().GroupKind(), gvk.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.Resource(mapping.Resource).Namespace(namespace).Create(context.Background(), obj, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionUpToDate(t *testing.T) {
	a, err := fixture.Abstraction()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateProjection(fixture.Package, adaptergen.FromAbstraction(a))
	if err != nil {
		t.Fatal(err)
	}
	assertGenerated(t, "projection.go", src)
}

func TestProject(t *testing.T) {
	ctx := context.Background()
	api, mapper := newAPI(t)
	state, err := Project(ctx, api, mapper)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"Database_db1_exists":                      false,
		"Database_db1_status_endpoint":             "",
		"DatabaseInstance_inst1_exists":            false,
		"DatabaseInstance_inst1_spec_databaseName": "",
		"DatabaseInstance_inst1_status_endpoint":   "",
	}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("empty API projects %v, want %v", state, want)
	}
	create(t, api, mapper, fixture.Database, "demo", "db-1", map[string]any{"status.endpoint": "db-1.internal"})
	create(t, api, mapper, fixture.Instance, "demo", "instance-for-db-1", map[string]any{"spec.databaseName": "db-1"})
	create(t, api, mapper, fixture.Database, "other", "db-1", map[string]any{"status.endpoint": "wrong-namespace"})
	state, err = Project(ctx, api, mapper)
	if err != nil {
		t.Fatal(err)
	}
	want["Database_db1_exists"] = true
	want["Database_db1_status_endpoint"] = "db-1.internal"
	want["DatabaseInstance_inst1_exists"] = true
	want["DatabaseInstance_inst1_spec_databaseName"] = "db-1"
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("populated API projects %v, want %v", state, want)
	}
}

func TestProjectRejectsWrongType(t *testing.T) {
	api, mapper := newAPI(t)
	create(t, api, mapper, fixture.Database, "demo", "db-1", map[string]any{"status.endpoint": int64(7)})
	_, err := Project(context.Background(), api, mapper)
	if err == nil || !strings.Contains(err.Error(), "Database_db1_status_endpoint") {
		t.Fatalf("expected a typed read error naming the state, got %v", err)
	}
}

func TestProjectRejectsUnknownKind(t *testing.T) {
	api, _ := newAPI(t)
	_, err := Project(context.Background(), api, meta.NewDefaultRESTMapper(nil))
	if err == nil || !meta.IsNoMatchError(err) {
		t.Fatalf("expected a no-match error from the mapper, got %v", err)
	}
}

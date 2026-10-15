package database

import (
	"context"
	"go/types"
	"reflect"
	"sort"
	"strings"
	"testing"

	"generative-architecture/examples/database/generated"

	"golang.org/x/tools/go/packages"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// projectionFixture stores a Database with the given contract annotation
// (none when empty) and endpoint, and optionally its instance, unstructured
// in a fake dynamic client.
func projectionFixture(t *testing.T, contract, endpoint string, instance bool) *dynamicfake.FakeDynamicClient {
	t.Helper()
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	database, err := e.Resources.Resolve("s.DatabaseResource")
	if err != nil {
		t.Fatal(err)
	}
	inst, err := e.Resources.Resolve("s.InstanceResource")
	if err != nil {
		t.Fatal(err)
	}
	db := &unstructured.Unstructured{Object: map[string]any{}}
	db.SetGroupVersionKind(database.GVK())
	db.SetNamespace(databaseKey.Namespace)
	db.SetName(databaseKey.Name)
	if contract != "" {
		db.SetAnnotations(map[string]string{"architecture.local/provisioning-contract": contract, "other.example/key": "v1"})
	}
	if endpoint != "" {
		if err := unstructured.SetNestedField(db.Object, endpoint, "status", "endpoint"); err != nil {
			t.Fatal(err)
		}
	}
	objects := []runtime.Object{db}
	if instance {
		i := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"databaseName": databaseKey.Name}, "status": map[string]any{"endpoint": "db-1.internal"}}}
		i.SetGroupVersionKind(inst.GVK())
		i.SetNamespace(databaseKey.Namespace)
		i.SetName("instance-for-" + databaseKey.Name)
		objects = append(objects, i)
	}
	return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
}

// The generated projection reads the Database and its instance through the
// dynamic client and derives contractSatisfied from the provisioning
// contract annotation: only the supported version satisfies it.
func TestGeneratedProjectionReadsDynamicFake(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	mapper, err := e.Resources.Mapper()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, contract, endpoint string
		instance, satisfied      bool
	}{
		{"supported contract", "v1", "db-1.internal", true, true},
		{"other contract version", "v2", "", false, false},
		{"no annotation", "", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, err := generated.Project(context.Background(), projectionFixture(t, tc.contract, tc.endpoint, tc.instance), mapper)
			if err != nil {
				t.Fatal(err)
			}
			instanceEndpoint, databaseName := "", ""
			if tc.instance {
				instanceEndpoint, databaseName = "db-1.internal", databaseKey.Name
			}
			want := map[string]any{
				"Database_db1_exists":                      true,
				"Database_db1_contractSatisfied":           tc.satisfied,
				"Database_db1_status_endpoint":             tc.endpoint,
				"DatabaseInstance_inst1_exists":            tc.instance,
				"DatabaseInstance_inst1_spec_databaseName": databaseName,
				"DatabaseInstance_inst1_status_endpoint":   instanceEndpoint,
			}
			if !reflect.DeepEqual(state, want) {
				t.Fatalf("projected %v, want %v", state, want)
			}
		})
	}

	// Absent objects project as not existing with zero values.
	state, err := generated.Project(context.Background(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), mapper)
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range state {
		if v != false && v != "" {
			t.Fatalf("absent objects project %s = %v", name, v)
		}
	}
}

// The adapter's API state comes from the generated projection; a projected
// instance whose reference names another Database is rejected.
func TestAdapterProjectsThroughGeneratedProjection(t *testing.T) {
	e, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := New(ctx, e, false)
	defer a.Close()
	if err := a.Apply(ctx, "discoverCompatible"); err != nil {
		t.Fatal(err)
	}
	state, err := a.projectAPI()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"databaseExists": true, "contractSatisfied": true, "statusEndpoint": "", "instanceExists": false, "instanceEndpoint": ""}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("projected %v, want %v", state, want)
	}
	inst, err := e.Resources.Resolve("s.InstanceResource")
	if err != nil {
		t.Fatal(err)
	}
	wrong := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"databaseName": "db-2"}}}
	wrong.SetGroupVersionKind(inst.GVK())
	wrong.SetNamespace(databaseKey.Namespace)
	wrong.SetName("instance-for-" + databaseKey.Name)
	if _, err := a.api.Resource(inst.GVR()).Namespace(databaseKey.Namespace).Create(ctx, wrong, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.projectAPI(); err == nil || !strings.Contains(err.Error(), "wrong Database") {
		t.Fatalf("wrong reference not rejected: %v", err)
	}
}

// The generated adapter code observes implementation state only (ADR-0005):
// neither it nor anything it imports reaches the packages that hold expected
// model state, and Project takes nothing but a context, a dynamic client and
// a REST mapper.
func TestGeneratedPackageReadsImplementationStateOnly(t *testing.T) {
	pkgs, err := packages.Load(&packages.Config{Dir: ".", Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedDeps}, "./generated")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || len(pkgs[0].Errors) > 0 || pkgs[0].Types == nil {
		t.Fatalf("generated package does not type-check: %+v", pkgs)
	}
	p := pkgs[0]
	forbidden := map[string]bool{"generative-architecture/pkg/model": true, "generative-architecture/pkg/conformance": true}
	var reached []string
	seen := map[*types.Package]bool{}
	var walk func(*types.Package, []string)
	walk = func(pkg *types.Package, via []string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		via = append(via, pkg.Path())
		if forbidden[pkg.Path()] || strings.HasPrefix(pkg.Path(), "generative-architecture/") && pkg != p.Types {
			reached = append(reached, strings.Join(via, " -> "))
		}
		for _, imp := range pkg.Imports() {
			walk(imp, via)
		}
	}
	walk(p.Types, nil)
	sort.Strings(reached)
	if len(reached) > 0 {
		t.Fatalf("generated package reaches repository packages that may hold expected model state: %v", reached)
	}
	if len(seen) < 5 {
		t.Fatalf("import walk saw only %d packages; type information is incomplete", len(seen))
	}
	fn, ok := p.Types.Scope().Lookup("Project").(*types.Func)
	if !ok {
		t.Fatal("generated package defines no Project function")
	}
	sig := fn.Type().(*types.Signature)
	var params []string
	for i := 0; i < sig.Params().Len(); i++ {
		params = append(params, sig.Params().At(i).Type().String())
	}
	want := []string{"context.Context", "k8s.io/client-go/dynamic.Interface", "k8s.io/apimachinery/pkg/api/meta.RESTMapper"}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("Project takes %v, want only a context and API clients %v", params, want)
	}
}

package configmirror

import (
	"context"
	"go/types"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/examples/configmirror/generated"

	"golang.org/x/tools/go/packages"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func projectionFakes(t *testing.T, api, cache map[client.ObjectKey]string) (client.Client, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	build := func(values map[client.ObjectKey]string) client.Client {
		b := fake.NewClientBuilder().WithScheme(scheme)
		for key, v := range values {
			b = b.WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}, Data: map[string]string{"value": v}})
		}
		return b.Build()
	}
	return build(api), build(cache)
}

// The generated projection reads the source and copy values from the API
// client and the source value a cached read would see from the cache reader.
func TestGeneratedProjectionReadsFakes(t *testing.T) {
	ctx := context.Background()
	api, cache := projectionFakes(t, map[client.ObjectKey]string{sourceKey: "b", targetKey: "a"}, map[client.ObjectKey]string{sourceKey: "a"})
	state, err := generated.Project(ctx, api, cache)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"ConfigMap_source_exists":            true,
		"ConfigMap_source_data_value":        "b",
		"cached_ConfigMap_source_exists":     true,
		"cached_ConfigMap_source_data_value": "a",
		"ConfigMap_copy_exists":              true,
		"ConfigMap_copy_data_value":          "a",
	}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("projected %v, want %v", state, want)
	}

	// An object absent from a reader projects as not existing with a zero
	// value; the projection does not fail.
	api, cache = projectionFakes(t, map[client.ObjectKey]string{sourceKey: "a"}, nil)
	state, err = generated.Project(ctx, api, cache)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]any{
		"ConfigMap_source_exists":            true,
		"ConfigMap_source_data_value":        "a",
		"cached_ConfigMap_source_exists":     false,
		"cached_ConfigMap_source_data_value": "",
		"ConfigMap_copy_exists":              false,
		"ConfigMap_copy_data_value":          "",
	}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("projected %v, want %v", state, want)
	}
}

// loadGenerated type-checks the generated package.
func loadGenerated(t *testing.T) *packages.Package {
	t.Helper()
	pkgs, err := packages.Load(&packages.Config{Dir: ".", Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedDeps}, "./generated")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || len(pkgs[0].Errors) > 0 || pkgs[0].Types == nil {
		t.Fatalf("generated package does not type-check: %+v", pkgs)
	}
	return pkgs[0]
}

// The generated adapter code observes implementation state only (ADR-0005):
// neither it nor anything it imports reaches the packages that hold expected
// model state, and Project takes nothing but a context and API readers.
func TestGeneratedPackageReadsImplementationStateOnly(t *testing.T) {
	p := loadGenerated(t)
	forbidden := map[string]bool{"github.com/phoban01/generative-architecture/pkg/model": true, "github.com/phoban01/generative-architecture/pkg/conformance": true}
	var reached []string
	seen := map[*types.Package]bool{}
	var walk func(*types.Package, []string)
	walk = func(pkg *types.Package, via []string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		via = append(via, pkg.Path())
		if forbidden[pkg.Path()] {
			reached = append(reached, strings.Join(via, " -> "))
		}
		for _, imp := range pkg.Imports() {
			walk(imp, via)
		}
	}
	walk(p.Types, nil)
	sort.Strings(reached)
	if len(reached) > 0 {
		t.Fatalf("generated package reaches expected model state: %v", reached)
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
	want := []string{"context.Context", "sigs.k8s.io/controller-runtime/pkg/client.Reader", "sigs.k8s.io/controller-runtime/pkg/client.Reader"}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("Project takes %v, want only a context and API readers %v", params, want)
	}
}

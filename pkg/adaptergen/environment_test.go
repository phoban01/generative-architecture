package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/fixture"
	"github.com/phoban01/generative-architecture/pkg/ir"
)

func TestGenerateEnvironment(t *testing.T) {
	src, err := adaptergen.GenerateEnvironment(fixture.Package, fixture.Actions())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "environment", src)
	p := typecheck(t, src)
	params, results := signature(t, p, "ApplyEnvironment")
	wantParams := []string{"context.Context", "k8s.io/client-go/dynamic.Interface", "k8s.io/apimachinery/pkg/api/meta.RESTMapper", "string"}
	if !reflect.DeepEqual(params, wantParams) {
		t.Errorf("ApplyEnvironment takes %v, want %v", params, wantParams)
	}
	if !reflect.DeepEqual(results, []string{"bool", "error"}) {
		t.Errorf("ApplyEnvironment returns %v", results)
	}
	assertNoModelState(t, p)
	text := string(src)
	for _, want := range []string{
		`case "discoverDatabase":`,
		`client.Create(ctx, obj, metav1.CreateOptions{})`,
		`unstructured.SetNestedField(obj.Object, "small", "spec", "size")`,
		`client.Update(ctx, obj, metav1.UpdateOptions{})`,
		`client.UpdateStatus(ctx, obj, metav1.UpdateOptions{})`,
		`client.Delete(ctx, "db-1", metav1.DeleteOptions{})`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated source lacks %q", want)
		}
	}
}

func TestGenerateEnvironmentClusterScoped(t *testing.T) {
	src, err := adaptergen.GenerateEnvironment("p", []adaptergen.EnvAction{
		{Name: "label", Operation: adaptergen.ActionUpdate, Resource: ir.GVK{Version: "v1", Kind: "Namespace"}, Key: ir.ObjectRef{Name: "demo"}, Fields: map[string]string{"metadata.labels.tier": "test", "metadata.labels.env": "ci"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	env := strings.Index(text, `"ci", "metadata", "labels", "env"`)
	tier := strings.Index(text, `"test", "metadata", "labels", "tier"`)
	if env < 0 || tier < 0 || env > tier {
		t.Fatalf("fields must be set in sorted path order:\n%s", text)
	}
	if !strings.Contains(text, `environmentClient(api, mapper, schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}, "")`) {
		t.Fatalf("cluster-scoped key must resolve without a namespace:\n%s", text)
	}
	typecheck(t, src)
}

func TestGenerateEnvironmentRejects(t *testing.T) {
	key := ir.ObjectRef{Namespace: "demo", Name: "db-1"}
	create := adaptergen.EnvAction{Name: "make", Operation: adaptergen.ActionCreate, Resource: fixture.Database, Key: key}
	cases := map[string]struct {
		pkg     string
		actions []adaptergen.EnvAction
		want    string
	}{
		"no actions":         {"p", nil, "at least one environment action"},
		"bad package":        {"1p", []adaptergen.EnvAction{create}, "not a Go identifier"},
		"empty name":         {"p", []adaptergen.EnvAction{{Operation: adaptergen.ActionCreate, Resource: fixture.Database, Key: key}}, "no name"},
		"duplicate name":     {"p", []adaptergen.EnvAction{create, create}, "declared twice"},
		"unknown operation":  {"p", []adaptergen.EnvAction{{Name: "x", Operation: "patch", Resource: fixture.Database, Key: key}}, "unknown operation"},
		"missing name":       {"p", []adaptergen.EnvAction{{Name: "x", Operation: adaptergen.ActionDelete, Resource: fixture.Database}}, "no name"},
		"missing kind":       {"p", []adaptergen.EnvAction{{Name: "x", Operation: adaptergen.ActionDelete, Key: key}}, "version and kind are required"},
		"other cluster":      {"p", []adaptergen.EnvAction{{Name: "x", Operation: adaptergen.ActionDelete, Resource: fixture.Database, Key: ir.ObjectRef{Cluster: "east", Namespace: "demo", Name: "n"}}}, "not supported"},
		"delete with fields": {"p", []adaptergen.EnvAction{{Name: "x", Operation: adaptergen.ActionDelete, Resource: fixture.Database, Key: key, Fields: map[string]string{"spec.size": "s"}}}, "delete sets no fields"},
		"empty path":         {"p", []adaptergen.EnvAction{{Name: "x", Operation: adaptergen.ActionUpdate, Resource: fixture.Database, Key: key, Fields: map[string]string{"": "s"}}}, "empty field path"},
		"empty path element": {"p", []adaptergen.EnvAction{{Name: "x", Operation: adaptergen.ActionUpdate, Resource: fixture.Database, Key: key, Fields: map[string]string{"spec..size": "s"}}}, "empty element"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := adaptergen.GenerateEnvironment(c.pkg, c.actions)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want %q", err, c.want)
			}
		})
	}
}

package abstraction

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

// A double-quoted map key may end any path of two or more elements: in a
// named field, a reference and an identity path. JSONPath unquotes it.
func TestQuotedMapKeys(t *testing.T) {
	a, err := Load("testdata/mapkeys.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cm := a.Spec.Resources[0]
	if got := JSONPath(cm.Fields[0].Path); !reflect.DeepEqual(got, []string{"data", "app.config/key"}) {
		t.Fatalf("field JSONPath %q", got)
	}
	if got := JSONPath(cm.References[0].Path); !reflect.DeepEqual(got, []string{"data", "app.config/target"}) {
		t.Fatalf("reference JSONPath %q", got)
	}
	if got := JSONPath(a.Spec.Resources[1].Identity[0]); !reflect.DeepEqual(got, []string{"metadata", "labels", "app.kubernetes.io/name"}) {
		t.Fatalf("identity JSONPath %q", got)
	}
	if _, ok := a.StateVariables()["ConfigMap_config_appKey"]; !ok {
		t.Fatalf("derived %v", a.SortedVariables())
	}
	// JSONPath copies: the document keeps its quoted form.
	if cm.Fields[0].Path[1] != `"app.config/key"` {
		t.Fatalf("path changed to %q", cm.Fields[0].Path)
	}
	if key, ok := MapKey(`"a\"b"`); !ok || key != `a"b` {
		t.Fatalf("escaped key %q %v", key, ok)
	}
}

func TestMapKeyFailures(t *testing.T) {
	db := ir.GVK{Group: "g", Version: "v1", Kind: "Database"}
	for name, c := range map[string]struct {
		mutate func(*Abstraction)
		want   string
	}{
		"unquoted key in a field": {func(a *Abstraction) {
			a.Spec.Resources[0].Fields = append(a.Spec.Resources[0].Fields, Field{Path: []string{"data", "app.config/key"}, Name: "appKey", Type: TypeString})
		}, `fields[1]: path[1]: element "app.config/key" must be an identifier; write a map key double-quoted, as '"app.config/key"' in YAML`},
		"quoted key without a name": {func(a *Abstraction) {
			a.Spec.Resources[0].Fields = append(a.Spec.Resources[0].Fields, Field{Path: []string{"data", `"app.config/key"`}, Type: TypeString})
		}, "holds a map key and needs a name"},
		"quoted key not last": {func(a *Abstraction) {
			a.Spec.Resources[0].Fields = append(a.Spec.Resources[0].Fields, Field{Path: []string{"data", `"k"`, "x"}, Name: "k", Type: TypeString})
		}, `fields[1]: path[1]: map key "k" must be the last element`},
		"quoted key first": {func(a *Abstraction) {
			a.Spec.Resources[0].Fields = append(a.Spec.Resources[0].Fields, Field{Path: []string{`"k"`}, Name: "k", Type: TypeString})
		}, "path[0]: map key"},
		"empty quoted key": {func(a *Abstraction) {
			a.Spec.Resources[0].Fields = append(a.Spec.Resources[0].Fields, Field{Path: []string{"data", `""`}, Name: "k", Type: TypeString})
		}, "not a non-empty double-quoted string"},
		"unterminated quote": {func(a *Abstraction) {
			a.Spec.Resources[0].Fields = append(a.Spec.Resources[0].Fields, Field{Path: []string{"data", `"k`}, Name: "k", Type: TypeString})
		}, "not a non-empty double-quoted string"},
		"unquoted key in a reference": {func(a *Abstraction) {
			a.Spec.Resources[0].References = []Reference{{Path: []string{"data", "a/b"}, TargetGVK: db}}
		}, `references[0]: path[1]: element "a/b" must be an identifier`},
		"unquoted key in identity": {func(a *Abstraction) {
			a.Spec.Resources[0].Identity = [][]string{{"metadata", "name"}, {"spec", "a.b"}}
		}, `identity[1]: path[1]: element "a.b" must be an identifier`},
	} {
		t.Run(name, func(t *testing.T) {
			a := valid()
			c.mutate(&a)
			err := a.Validate()
			// The error names the document and resource as well as the
			// element.
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "abstraction x, resource ") {
				t.Fatalf("got %v, want error containing %q", err, c.want)
			}
		})
	}
}

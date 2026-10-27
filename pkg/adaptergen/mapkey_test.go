package adaptergen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/adaptergen"
	"github.com/phoban01/generative-architecture/pkg/adaptergen/internal/fixture"
)

// A quoted map key in an abstraction path reaches generated code as the
// plain key: projections, environment actions, reader environments and
// reference checks address data["app.config/key"], never the quoted form.
func TestGeneratorsUnquoteMapKeys(t *testing.T) {
	a, err := abstraction.Load("../abstraction/testdata/mapkeys.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const key, quoted = `"data", "app.config/key"`, `\"app.config/key\"`
	check := func(what string, src []byte, want string) {
		t.Helper()
		typecheck(t, src)
		if !strings.Contains(string(src), want) || strings.Contains(string(src), quoted) || strings.Contains(string(src), `\"app.config/target\"`) {
			t.Errorf("%s lacks %s or keeps the quoted key:\n%s", what, want, src)
		}
	}

	var found bool
	for _, p := range adaptergen.FromAbstraction(a) {
		if p.State == "ConfigMap_config_appKey" {
			found = reflect.DeepEqual(p.JSONPath, []string{"data", "app.config/key"})
		}
	}
	if !found {
		t.Fatal("no projection of data[app.config/key]")
	}
	src, err := adaptergen.GenerateProjection(fixture.Package, adaptergen.FromAbstraction(a))
	if err != nil {
		t.Fatal(err)
	}
	check("projection", src, key)

	action, err := adaptergen.FieldAction(a.Spec, "setKey", adaptergen.ActionUpdate, "config", "ConfigMap_config_appKey", `"on"`)
	if err != nil {
		t.Fatal(err)
	}
	if src, err = adaptergen.GenerateEnvironment(fixture.Package, []adaptergen.EnvAction{action}); err != nil {
		t.Fatal(err)
	}
	check("environment", src, `"on", `+key)

	if src, err = adaptergen.GenerateReaderEnvironment(fixture.Package, a.Spec, []string{"config"}, []string{"config"}); err != nil {
		t.Fatal(err)
	}
	check("reader environment", src, key)

	// Reference checks support only metadata.name identity, so the target
	// is identified by name here.
	a.Spec.Resources[1].Identity = [][]string{{"metadata", "name"}}
	if src, err = adaptergen.GenerateReferenceCheck(fixture.Package, a.Spec); err != nil {
		t.Fatal(err)
	}
	check("reference check", src, `[]string{"data", "app.config/target"}, "Secret"`)
}

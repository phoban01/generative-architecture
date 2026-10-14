package watchgen_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/watchgen"
)

func load(t *testing.T, pkg, receiver string) goflow.Loaded {
	t.Helper()
	l, err := goflow.Load("../..", goflow.Selector{Package: pkg, Receiver: receiver, Method: "SetupWithManager"})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestExtractLoadedAgreesWithTextExtraction(t *testing.T) {
	source, resources := fixture(t)
	text, err := watchgen.Extract(source, "DatabaseReconciler", "SetupWithManager", resources)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := watchgen.ExtractLoaded(load(t, "./examples/database", "DatabaseReconciler"), resources, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(text, loaded) {
		t.Fatalf("loaded extraction differs from text extraction:\n%+v\n%+v", text, loaded)
	}
	if loaded.SourceSHA256 != goflow.Hash(source) || loaded.MethodSHA256 == "" || len(loaded.Rules) != 2 {
		t.Fatalf("hashes or rules missing: %+v", loaded)
	}
}

func TestExtractLoadedResolvesUnaliasedImports(t *testing.T) {
	source, err := os.ReadFile("testdata/unaliased/wiring.go")
	if err != nil {
		t.Fatal(err)
	}
	resources := map[string]effect.Resource{
		"k8s.io/api/core/v1.ConfigMap": {Version: "v1", Kind: "ConfigMap"},
		"k8s.io/api/core/v1.Pod":       {Version: "v1", Kind: "Pod"},
	}
	// Raw source has no alias for k8s.io/apimachinery/pkg/types, which the
	// text path cannot resolve.
	_, err = watchgen.Extract(source, "Reconciler", "SetupWithManager", resources)
	if diag, ok := goflow.AsDiagnostic(err); !ok || diag.Construct != "import" || !strings.Contains(diag.Reason, "k8s.io/apimachinery/pkg/types") {
		t.Fatalf("text path should require an alias, got %v", err)
	}
	result, err := watchgen.ExtractLoaded(load(t, "./pkg/watchgen/testdata/unaliased", "Reconciler"), resources, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rules) != 2 || result.Rules[0].Resource.Kind != "ConfigMap" || result.Rules[0].Mapping != "self" {
		t.Fatalf("primary watch not extracted: %+v", result.Rules)
	}
	pods := result.Rules[1]
	if pods.Resource.Kind != "Pod" || pods.GoType != "k8s.io/api/core/v1.Pod" || pods.Mapping != "PodToConfigMap" || strings.Join(pods.NamePath, ".") != "Spec.ServiceAccountName" || pods.MappingSHA256 == "" {
		t.Fatalf("mapper through an unaliased key type not extracted: %+v", pods)
	}
	if pods.Line == 0 || result.SourceSHA256 != goflow.Hash(source) {
		t.Fatalf("location or hash missing: %+v", result)
	}
}

func TestExtractLoadedRequiresTypeInformation(t *testing.T) {
	l := load(t, "./examples/database", "DatabaseReconciler")
	l.Package = nil
	if _, err := watchgen.ExtractLoaded(l, nil, nil); err == nil || !strings.Contains(err.Error(), "type information") {
		t.Fatalf("expected a type-information error, got %v", err)
	}
}

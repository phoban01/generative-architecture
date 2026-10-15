package configmirror

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"generative-architecture/pkg/abstraction"
	"generative-architecture/pkg/adaptergen"
)

// The replay adapter's projection, environment and gating are generated
// from the authored inputs. This test is the generator entry point: run
//
//	go test ./examples/configmirror -run TestAdapterGeneratedFilesUpToDate -regenerate-adapter
//
// to re-emit generated/*_generated.go; without the flag it fails when a
// committed file differs from what the generators produce.
var regenerateAdapter = flag.Bool("regenerate-adapter", false, "rewrite the generated replay adapter files")

// generatedPackage is the package the adapter files are generated into.
const generatedPackage = "generated"

// cachedObjects are the abstraction objects the reconciler reads through
// its cache-backed client.
var cachedObjects = []string{"source"}

// adapterProjection generates the projection from the abstraction document:
// every object's fields through the API, and the cached objects' fields
// also through the cache reader.
func adapterProjection() ([]byte, error) {
	a, err := abstraction.Load("abstraction.yaml")
	if err != nil {
		return nil, err
	}
	ps, err := adaptergen.FromSpec(a.Spec, cachedObjects...)
	if err != nil {
		return nil, err
	}
	return adaptergen.GenerateReaderProjection(generatedPackage, ps)
}

func TestAdapterGeneratedFilesUpToDate(t *testing.T) {
	for file, generate := range map[string]func() ([]byte, error){
		"projection_generated.go": adapterProjection,
	} {
		t.Run(file, func(t *testing.T) {
			src, err := generate()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("generated", file)
			if *regenerateAdapter {
				if err := os.WriteFile(path, src, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			have, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(have) != string(src) {
				t.Fatalf("%s is stale; run go test ./examples/configmirror -run TestAdapterGeneratedFilesUpToDate -regenerate-adapter", path)
			}
		})
	}
}

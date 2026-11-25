package refsystem

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The Flux boundary the project document names is a copy of the library's,
// because a project document's paths stay inside its directory.
func TestFluxBoundaryMatchesLibrary(t *testing.T) {
	for _, name := range []string{"kustomization.yaml", "kustomization-model.yaml"} {
		local, err := os.ReadFile(filepath.Join("boundaries", "flux", name))
		if err != nil {
			t.Fatal(err)
		}
		library, err := os.ReadFile(filepath.Join("..", "..", "pkg", "boundary", "library", "flux", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(local, library) {
			t.Errorf("boundaries/flux/%s differs from pkg/boundary/library/flux/%s; copy the library file", name, name)
		}
	}
}

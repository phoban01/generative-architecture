package boundary_test

import (
	"github.com/phoban01/generative-architecture/pkg/boundary"
	"github.com/phoban01/generative-architecture/pkg/composition"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"github.com/phoban01/generative-architecture/pkg/model"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// system is a composition of library boundaries with one owned fixture
// controller named consumer.
type system struct {
	// boundaries maps a component name to a library document, relative to
	// library/.
	boundaries map[string]string
	consumer   model.Spec
	ports      map[string]composition.Port
	links      []composition.Link
}

// compose copies the library into a fresh root, so the composition reads it
// as project files, and loads the composition from there.
func (s system) compose(t *testing.T) (composition.Loaded, error) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "library"), os.DirFS("library")); err != nil {
		t.Fatal(err)
	}
	if err := krm.Write(filepath.Join(root, "consumer.yaml"), "TransitionModel", "consumer", s.consumer); err != nil {
		t.Fatal(err)
	}
	doc := composition.Document{Name: "LibrarySystem", AutoLink: true, Links: s.links, Properties: []model.Property{}}
	for _, name := range sortedNames(s.boundaries) {
		doc.Components = append(doc.Components, composition.ComponentRef{Name: name, Boundary: filepath.Join("library", s.boundaries[name])})
	}
	doc.Components = append(doc.Components, composition.ComponentRef{Name: "consumer", Model: "consumer.yaml", Ports: s.ports})
	if err := krm.Write(filepath.Join(root, "system.yaml"), "SystemComposition", "system", doc); err != nil {
		t.Fatal(err)
	}
	return composition.Load(root, "system.yaml")
}

// run fires events on the composed model and checks every property after
// each one. It stops at the first disabled action or failed property.
func run(t *testing.T, spec model.Spec, events ...string) (map[string]any, error) {
	t.Helper()
	m, err := model.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Initial()
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if state, err = m.Next(state, event); err != nil {
			return state, err
		}
		if err = m.Check(state, event); err != nil {
			return state, err
		}
	}
	return state, nil
}

// loadLibrary loads one library document and checks the facts every library
// boundary shares: a pinned version, a compiling model, and assumed IR facts
// whose provenance carries the pin.
func loadLibrary(t *testing.T, path string) boundary.Boundary {
	t.Helper()
	b, err := boundary.Load(filepath.Join("library", path))
	if err != nil {
		t.Fatal(err)
	}
	if b.Model == nil {
		t.Fatalf("%s: no model", path)
	}
	if _, err := model.Compile(*b.Model); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	watches, effects := b.Facts(b.Name)
	for _, w := range watches {
		if err := w.Validate(); err != nil || w.Evidence != ir.Assumed || w.Provenance.ModuleVersion != b.Spec.Version {
			t.Errorf("%s: watch %+v: %v", path, w, err)
		}
	}
	for _, e := range effects {
		if err := e.Validate(); err != nil || e.Evidence != ir.Assumed || e.Provenance.ModuleVersion != b.Spec.Version {
			t.Errorf("%s: effect %+v: %v", path, e, err)
		}
	}
	return b
}

func sortedNames(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

package summaries

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/phoban01/generative-architecture/pkg/krm"
)

// Kind is the KRM kind of a summary library document. One document holds
// any number of summaries.
const Kind = "EffectSummaries"

// Library is the spec of an EffectSummaries document.
type Library struct {
	Summaries []Summary `yaml:"summaries"`
}

// Built-in summaries. Each *.yaml file is one EffectSummaries document.
//
//go:embed data
var builtin embed.FS

type key struct{ operation, consistency string }

// Registry resolves an operation and consistency to its summary.
type Registry struct{ summaries map[key]Summary }

// Add registers a validated summary; a duplicate operation/consistency is
// an error.
func (r *Registry) Add(s Summary) error {
	if err := s.Validate(); err != nil {
		return err
	}
	k := key{s.Operation, s.Consistency}
	if _, dup := r.summaries[k]; dup {
		return fmt.Errorf("summary %s/%s registered twice", s.Operation, s.Consistency)
	}
	if r.summaries == nil {
		r.summaries = map[key]Summary{}
	}
	r.summaries[k] = s
	return nil
}

// Lookup returns the summary for an operation (see OperationKey) at a
// consistency, or an error naming what is missing.
func (r Registry) Lookup(operation, consistency string) (Summary, error) {
	s, ok := r.summaries[key{operation, consistency}]
	if !ok {
		return Summary{}, fmt.Errorf("no summary for operation %q at consistency %q; known: %v", operation, consistency, r.Keys())
	}
	return s, nil
}

// Keys lists the registered operation/consistency pairs in sorted order.
func (r Registry) Keys() []string {
	out := make([]string, 0, len(r.summaries))
	for k := range r.summaries {
		out = append(out, k.operation+"/"+k.consistency)
	}
	sort.Strings(out)
	return out
}

// Builtin returns the registry of embedded summaries. The data is part of
// the binary and covered by this package's tests, so a malformed file is a
// programming error and panics rather than returning an error to every
// caller.
func Builtin() Registry {
	r, err := Load(builtin, "data")
	if err != nil {
		panic(fmt.Sprintf("summaries: embedded library: %v", err))
	}
	return r
}

// Load reads every *.yaml EffectSummaries document directly under dir of
// fsys into a registry.
func Load(fsys fs.FS, dir string) (Registry, error) {
	var r Registry
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return r, err
	}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".yaml" {
			continue
		}
		name := path.Join(dir, e.Name())
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return r, err
		}
		lib, err := decode(b)
		if err != nil {
			return r, fmt.Errorf("%s: %w", name, err)
		}
		for _, s := range lib.Summaries {
			if err := r.Add(s); err != nil {
				return r, fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return r, nil
}

// decode parses one EffectSummaries document from memory with the KRM
// loader's strictness (known fields, no aliases, one document).
func decode(b []byte) (Library, error) {
	var doc krm.Resource[Library]
	if err := krm.DecodeYAML(b, &doc); err != nil {
		return Library{}, err
	}
	if doc.APIVersion != krm.APIVersion || doc.Kind != Kind {
		return Library{}, fmt.Errorf("expected %s %s, got %s %s", krm.APIVersion, Kind, doc.APIVersion, doc.Kind)
	}
	if doc.Metadata.Name == "" {
		return Library{}, fmt.Errorf("metadata.name is required")
	}
	if doc.Spec == nil || doc.Status != nil {
		return Library{}, fmt.Errorf("expected only spec payload")
	}
	return *doc.Spec, nil
}

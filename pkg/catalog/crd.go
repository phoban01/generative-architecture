package catalog

import (
	"errors"
	"fmt"
	"io"
	"os"

	"generative-architecture/pkg/ir"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// LoadCRDs reads apiextensions.k8s.io/v1 CustomResourceDefinition manifests
// (multi-document files allowed) and returns one entry per served version.
// Origin is the file path.
func LoadCRDs(paths ...string) ([]Entry, error) {
	var out []Entry
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		entries, err := decodeCRDs(f, path)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, entries...)
	}
	return out, nil
}

func decodeCRDs(r io.Reader, origin string) ([]Entry, error) {
	var out []Entry
	d := utilyaml.NewYAMLOrJSONDecoder(r, 4096)
	for i := 0; ; i++ {
		var crd apiextensionsv1.CustomResourceDefinition
		err := d.Decode(&crd)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", i, err)
		}
		if crd.Kind == "" && crd.APIVersion == "" && crd.Spec.Group == "" {
			continue // empty document
		}
		if crd.APIVersion != apiextensionsv1.SchemeGroupVersion.String() || crd.Kind != "CustomResourceDefinition" {
			return nil, fmt.Errorf("document %d: expected %s CustomResourceDefinition, got %s %s", i, apiextensionsv1.SchemeGroupVersion, crd.APIVersion, crd.Kind)
		}
		if crd.Spec.Names.Plural == "" || crd.Spec.Names.Kind == "" {
			return nil, fmt.Errorf("document %d: CRD %s has no plural or kind", i, crd.Name)
		}
		scope := ir.Namespaced
		if crd.Spec.Scope == apiextensionsv1.ClusterScoped {
			scope = ir.Cluster
		}
		for _, v := range crd.Spec.Versions {
			if !v.Served {
				continue
			}
			e := Entry{
				GVK:     ir.GVK{Group: crd.Spec.Group, Version: v.Name, Kind: crd.Spec.Names.Kind},
				GVR:     ir.GVR{Group: crd.Spec.Group, Version: v.Name, Resource: crd.Spec.Names.Plural},
				Scope:   scope,
				Served:  true,
				Storage: v.Storage,
				Origin:  origin,
			}
			if err := e.Validate(); err != nil {
				return nil, fmt.Errorf("document %d: %w", i, err)
			}
			out = append(out, e)
		}
	}
}

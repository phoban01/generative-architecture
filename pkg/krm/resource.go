// Package krm defines local, versioned Kubernetes Resource Model documents.
// These are not registered CRDs and do not require a Kubernetes API server.
package krm

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/validation"
	"os"
)

const APIVersion = "architecture.local/v1alpha1"

type Metadata struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}
type Resource[T any] struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       *T       `yaml:"spec,omitempty"`
	Status     *T       `yaml:"status,omitempty"`
}

func validate[T any](r Resource[T], kind string) error {
	if r.APIVersion != APIVersion || r.Kind != kind {
		return fmt.Errorf("expected %s %s, got %s %s", APIVersion, kind, r.APIVersion, r.Kind)
	}
	if e := validation.IsDNS1123Subdomain(r.Metadata.Name); len(e) > 0 {
		return fmt.Errorf("invalid metadata.name: %v", e)
	}
	// Local architecture identities are cluster-scoped. Reject namespaces rather than ignoring them.
	if r.Metadata.Namespace != "" {
		return fmt.Errorf("architecture documents do not use metadata.namespace")
	}
	for k, v := range r.Metadata.Labels {
		if len(validation.IsQualifiedName(k)) > 0 || len(validation.IsValidLabelValue(v)) > 0 {
			return fmt.Errorf("invalid label %q", k)
		}
	}
	for k := range r.Metadata.Annotations {
		if len(validation.IsQualifiedName(k)) > 0 {
			return fmt.Errorf("invalid annotation %q", k)
		}
	}
	return nil
}
func read[T any](path, kind string, out *T, result bool) (Metadata, error) {
	var r Resource[T]
	b, err := os.ReadFile(path)
	if err != nil {
		return r.Metadata, err
	}
	if err = DecodeYAML(b, &r); err != nil {
		return r.Metadata, fmt.Errorf("%s: %w", path, err)
	}
	if err = validate(r, kind); err != nil {
		return r.Metadata, fmt.Errorf("%s: %w", path, err)
	}
	v, other := r.Spec, r.Status
	field := "spec"
	if result {
		v, other = r.Status, r.Spec
		field = "status"
	}
	if v == nil || other != nil {
		return r.Metadata, fmt.Errorf("%s: expected only %s payload", path, field)
	}
	*out = *v
	return r.Metadata, nil
}
func Read[T any](path, kind string, out *T) (Metadata, error) { return read(path, kind, out, false) }
func ReadResult[T any](path, kind string, out *T) error {
	_, err := read(path, kind, out, true)
	return err
}
func write(path, kind, name string, value any, result bool) error {
	r := Resource[any]{APIVersion: APIVersion, Kind: kind, Metadata: Metadata{Name: name}}
	if result {
		r.Status = &value
	} else {
		r.Spec = &value
	}
	if err := validate(r, kind); err != nil {
		return err
	}
	return WriteYAML(path, r)
}
func Write(path, kind, name string, value any) error { return write(path, kind, name, value, false) }
func WriteResult(path, kind, name string, value any) error {
	return write(path, kind, name, value, true)
}

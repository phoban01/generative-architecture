// Package ir is the shared intermediate representation that every frontend
// emits and every generator consumes. Facts carry an evidence kind and a
// provenance record so receipts can say where each one came from.
package ir

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ClusterID names the cluster an object lives in. The empty ClusterID is the
// single implicit cluster of a one-cluster system.
type ClusterID string

// GVK identifies an API kind.
type GVK struct {
	Group   string `yaml:"group,omitempty"`
	Version string `yaml:"version"`
	Kind    string `yaml:"kind"`
}

// GVR identifies an API resource.
type GVR struct {
	Group    string `yaml:"group,omitempty"`
	Version  string `yaml:"version"`
	Resource string `yaml:"resource"`
}

// Scope says whether a resource is namespaced or cluster-wide.
type Scope string

const (
	Namespaced Scope = "Namespaced"
	Cluster    Scope = "Cluster"
)

// ObjectRef identifies one object: cluster, kind, namespace, name and, when
// known, UID. Two refs that differ only by Cluster are different objects.
type ObjectRef struct {
	Cluster   ClusterID `yaml:"cluster,omitempty"`
	GVK       GVK       `yaml:"gvk"`
	Namespace string    `yaml:"namespace,omitempty"`
	Name      string    `yaml:"name"`
	UID       string    `yaml:"uid,omitempty"`
}

func (g GVK) String() string {
	if g.Group == "" {
		return g.Version + "/" + g.Kind
	}
	return g.Group + "/" + g.Version + "/" + g.Kind
}

func (g GVR) String() string {
	if g.Group == "" {
		return g.Version + "/" + g.Resource
	}
	return g.Group + "/" + g.Version + "/" + g.Resource
}

func (s Scope) String() string { return string(s) }

func (r ObjectRef) String() string {
	s := r.GVK.String() + " "
	if r.Namespace != "" {
		s += r.Namespace + "/"
	}
	s += r.Name
	if r.Cluster != "" {
		s = string(r.Cluster) + ":" + s
	}
	if r.UID != "" {
		s += " (" + r.UID + ")"
	}
	return s
}

// Validate requires a version and kind.
func (g GVK) Validate() error {
	if g.Version == "" || g.Kind == "" {
		return fmt.Errorf("GVK %q: version and kind are required", g.String())
	}
	return nil
}

// Validate requires a version and resource.
func (g GVR) Validate() error {
	if g.Version == "" || g.Resource == "" {
		return fmt.Errorf("GVR %q: version and resource are required", g.String())
	}
	return nil
}

// Validate accepts Namespaced, Cluster or the empty (unknown) scope.
func (s Scope) Validate() error {
	switch s {
	case "", Namespaced, Cluster:
		return nil
	}
	return fmt.Errorf("unknown scope %q", string(s))
}

// Validate requires a valid GVK and a name. When scope is known, a namespace
// is forbidden for cluster-scoped objects and required for namespaced ones.
func (r ObjectRef) Validate(scope Scope) error {
	if err := r.GVK.Validate(); err != nil {
		return err
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	if r.Name == "" {
		return fmt.Errorf("object %s: name is required", r.String())
	}
	if scope == Cluster && r.Namespace != "" {
		return fmt.Errorf("object %s: cluster-scoped objects have no namespace", r.String())
	}
	if scope == Namespaced && r.Namespace == "" {
		return fmt.Errorf("object %s: namespaced objects require a namespace", r.String())
	}
	return nil
}

// FromSchemaGVK converts an apimachinery GroupVersionKind.
func FromSchemaGVK(g schema.GroupVersionKind) GVK {
	return GVK{Group: g.Group, Version: g.Version, Kind: g.Kind}
}

// Schema converts to an apimachinery GroupVersionKind.
func (g GVK) Schema() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: g.Group, Version: g.Version, Kind: g.Kind}
}

// FromSchemaGVR converts an apimachinery GroupVersionResource.
func FromSchemaGVR(g schema.GroupVersionResource) GVR {
	return GVR{Group: g.Group, Version: g.Version, Resource: g.Resource}
}

// Schema converts to an apimachinery GroupVersionResource.
func (g GVR) Schema() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g.Group, Version: g.Version, Resource: g.Resource}
}

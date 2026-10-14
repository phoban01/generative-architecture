package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the group and version of the reference-system API.
	GroupVersion = schema.GroupVersion{Group: "refsystem.example.org", Version: "v1alpha1"}

	// SchemeBuilder registers the API types with a runtime.Scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the API types to a runtime.Scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

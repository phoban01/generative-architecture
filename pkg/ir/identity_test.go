package ir

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIdentityStrings(t *testing.T) {
	gvk := GVK{Group: "apps", Version: "v1", Kind: "Deployment"}
	if gvk.String() != "apps/v1/Deployment" {
		t.Fatal(gvk.String())
	}
	if (GVK{Version: "v1", Kind: "ConfigMap"}).String() != "v1/ConfigMap" {
		t.Fatal("core group string")
	}
	if (GVR{Group: "apps", Version: "v1", Resource: "deployments"}).String() != "apps/v1/deployments" {
		t.Fatal("GVR string")
	}
	ref := ObjectRef{Cluster: "east", GVK: gvk, Namespace: "ns", Name: "web", UID: "u1"}
	if ref.String() != "east:apps/v1/Deployment ns/web (u1)" {
		t.Fatal(ref.String())
	}
	if (ObjectRef{GVK: GVK{Version: "v1", Kind: "Node"}, Name: "n1"}).String() != "v1/Node n1" {
		t.Fatal("cluster-scoped string")
	}
}

func TestValidate(t *testing.T) {
	if err := (GVK{Kind: "X"}).Validate(); err == nil {
		t.Fatal("missing version accepted")
	}
	if err := (GVR{Version: "v1"}).Validate(); err == nil {
		t.Fatal("missing resource accepted")
	}
	if err := Scope("Global").Validate(); err == nil {
		t.Fatal("unknown scope accepted")
	}
	ok := ObjectRef{GVK: GVK{Version: "v1", Kind: "ConfigMap"}, Namespace: "ns", Name: "c"}
	if err := ok.Validate(Namespaced); err != nil {
		t.Fatal(err)
	}
	if err := ok.Validate(""); err != nil {
		t.Fatal(err)
	}
	if err := ok.Validate(Cluster); err == nil {
		t.Fatal("namespace accepted for cluster scope")
	}
	nodeRef := ObjectRef{GVK: GVK{Version: "v1", Kind: "Node"}, Name: "n"}
	if err := nodeRef.Validate(Namespaced); err == nil {
		t.Fatal("missing namespace accepted for namespaced scope")
	}
	if err := (ObjectRef{GVK: ok.GVK, Namespace: "ns"}).Validate(Namespaced); err == nil {
		t.Fatal("missing name accepted")
	}
}

func TestClusterDistinguishesRefs(t *testing.T) {
	a := ObjectRef{Cluster: "east", GVK: GVK{Version: "v1", Kind: "ConfigMap"}, Namespace: "ns", Name: "c"}
	b := a
	b.Cluster = "west"
	if a == b {
		t.Fatal("refs differing only by cluster compare equal")
	}
	if a.String() == b.String() {
		t.Fatal("strings do not show the cluster")
	}
}

func TestSchemaConversions(t *testing.T) {
	in := schema.GroupVersionKind{Group: "g", Version: "v", Kind: "K"}
	if FromSchemaGVK(in).Schema() != in {
		t.Fatal("GVK round trip")
	}
	r := schema.GroupVersionResource{Group: "g", Version: "v", Resource: "ks"}
	if FromSchemaGVR(r).Schema() != r {
		t.Fatal("GVR round trip")
	}
}

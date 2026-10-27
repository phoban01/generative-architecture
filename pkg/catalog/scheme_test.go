package catalog

import (
	"strings"
	"testing"

	refv1 "github.com/phoban01/generative-architecture/examples/refsystem/api/v1alpha1"
	"github.com/phoban01/generative-architecture/pkg/ir"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestFromScheme(t *testing.T) {
	s := runtime.NewScheme()
	if err := refv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	gvk := func(kind string) ir.GVK {
		return ir.GVK{Group: "refsystem.example.org", Version: "v1alpha1", Kind: kind}
	}
	gvr := func(plural string) ir.GVR {
		return ir.GVR{Group: "refsystem.example.org", Version: "v1alpha1", Resource: plural}
	}
	plurals := map[ir.GVK]ir.GVR{gvk("Tenant"): gvr("tenants"), gvk("Database"): gvr("databases"), gvk("Backup"): gvr("backups")}
	scopes := map[ir.GVK]ir.Scope{gvk("Tenant"): ir.Namespaced, gvk("Database"): ir.Namespaced, gvk("Backup"): ir.Namespaced}
	entries, err := FromScheme(s, plurals, scopes)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].GVK.Kind != "Backup" || entries[1].GVK.Kind != "Database" || entries[2].GVK.Kind != "Tenant" {
		t.Fatalf("unexpected entries %+v", entries)
	}
	delete(plurals, gvk("Database"))
	_, err = FromScheme(s, plurals, scopes)
	if err == nil || !strings.Contains(err.Error(), "refsystem.example.org/v1alpha1/Database") {
		t.Fatalf("missing plural not reported by GVK: %v", err)
	}
	plurals[gvk("Database")] = gvr("databases")
	delete(scopes, gvk("Backup"))
	if _, err = FromScheme(s, plurals, scopes); err == nil || !strings.Contains(err.Error(), "Backup") {
		t.Fatalf("missing scope not reported: %v", err)
	}
}

package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestAddToSchemeRegistersEveryKind(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"Tenant", "Database", "Backup", "TenantList", "DatabaseList", "BackupList"} {
		if !s.Recognizes(GroupVersion.WithKind(kind)) {
			t.Fatalf("%s not registered", kind)
		}
	}
}

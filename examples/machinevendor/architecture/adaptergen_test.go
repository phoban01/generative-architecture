package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdapterGenerationIsCurrent(t *testing.T) {
	m := model(t)
	generated, err := m.AdapterGo()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(m.Project.Root, "generated/adapter_generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(generated) {
		t.Fatal("adapter wiring is stale; run arch generate")
	}
	if strings.Contains(string(generated), "observed") {
		t.Fatal("behavior leaked into adapter wiring")
	}
}

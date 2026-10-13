package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedMarkersAndUnmodifiedUpstreamTypes(t *testing.T) {
	root := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.org/types\n\ngo 1.25\n")
	source := "package types\n// +arch:implements=allocation/v1\ntype Status struct { MachineID string `json:\"machineId\"` }\ntype Upstream struct { Status Status `json:\"status\"` }\n"
	write("types.go", source)
	defs := []Definition{{Name: "allocation", Version: "v1", Fields: map[string]string{"machineId": "string"}}}
	evidence, err := Inspect(root, defs, []string{"."}, []Implementation{{Contract: "allocation/v1", Package: ".", Type: "Upstream", Fields: map[string]string{"machineId": "status.machineId"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 {
		t.Fatalf("%+v", evidence)
	}
	paths := map[string]bool{}
	for _, e := range evidence {
		paths[e.JSONPaths["machineId"]] = true
		if len(e.SourceHashes) < 2 {
			t.Fatal("source provenance missing")
		}
	}
	if !paths["machineId"] || !paths["status.machineId"] {
		t.Fatal(paths)
	}
	write("types.go", strings.Replace(source, "MachineID string", "MachineID bool", 1))
	if _, err = Inspect(root, defs, []string{"."}, nil); err == nil {
		t.Fatal("incompatible field drift accepted")
	}
	write("types.go", strings.Replace(source, "allocation/v1", "allocation/v2", 1))
	if _, err = Inspect(root, defs, []string{"."}, nil); err == nil {
		t.Fatal("unknown contract version accepted")
	}
}

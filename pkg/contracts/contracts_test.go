package contracts

import (
	"go/types"
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
	source := "package types\n// +arch:implements=endpoint/v1\ntype Status struct { Endpoint string `json:\"endpoint\"` }\ntype Upstream struct { Status Status `json:\"status\"` }\n"
	write("types.go", source)
	defs := []Definition{{Name: "endpoint", Version: "v1", Fields: map[string]string{"endpoint": "string"}}}
	evidence, err := Inspect(root, defs, []string{"."}, []Implementation{{Contract: "endpoint/v1", Package: ".", Type: "Upstream", Fields: map[string]string{"endpoint": "status.endpoint"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 {
		t.Fatalf("%+v", evidence)
	}
	paths := map[string]bool{}
	for _, e := range evidence {
		paths[e.JSONPaths["endpoint"]] = true
		if len(e.SourceHashes) < 2 {
			t.Fatal("source provenance missing")
		}
	}
	if !paths["endpoint"] || !paths["status.endpoint"] {
		t.Fatal(paths)
	}
	write("types.go", strings.Replace(source, "Endpoint string", "Endpoint bool", 1))
	if _, err = Inspect(root, defs, []string{"."}, nil); err == nil {
		t.Fatal("incompatible field drift accepted")
	}
	write("types.go", strings.Replace(source, "endpoint/v1", "endpoint/v2", 1))
	if _, err = Inspect(root, defs, []string{"."}, nil); err == nil {
		t.Fatal("unknown contract version accepted")
	}
}

func TestMarkerDiagnosticsAreLocated(t *testing.T) {
	defs := []Definition{{Name: "endpoint", Version: "v1", Fields: map[string]string{"endpoint": "string"}}}
	for _, tc := range []struct {
		name, source, wantErr string
	}{
		{name: "field drift", source: "package types\n// +arch:implements=endpoint/v1\ntype Status struct { Endpoint bool `json:\"endpoint\"` }\n", wantErr: "types.go:2:1: +arch:implements=endpoint/v1: field endpoint: expected string, got bool"},
		{name: "unknown version", source: "package types\n// +arch:implements=endpoint/v2\ntype Status struct { Endpoint string `json:\"endpoint\"` }\n", wantErr: "types.go:2:1: +arch:implements=endpoint/v2: unknown contract endpoint/v2"},
		{name: "missing field", source: "package types\n\n// +arch:implements=endpoint/v1\ntype Status struct { URL string `json:\"url\"` }\n", wantErr: "types.go:3:1: +arch:implements=endpoint/v1: field endpoint: missing contract field endpoint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, data := range map[string]string{"go.mod": "module example.org/types\n\ngo 1.25\n", "types.go": tc.source} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Inspect(root, defs, []string{"."}, nil)
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("got error %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestDefinitionValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		def     Definition
		wantErr string
	}{
		{name: "primitives", def: Definition{Name: "c", Version: "v1", Fields: map[string]string{"a": "string", "b": "bool", "c": "int", "d": "int32"}}},
		{name: "nullable and optional", def: Definition{Name: "c", Version: "v1", Fields: map[string]string{"a": "*string"}, Optional: map[string]string{"b": "*bool"}}},
		{name: "no required fields", def: Definition{Name: "c", Version: "v1", Optional: map[string]string{"b": "bool"}}, wantErr: "name, version and fields are required"},
		{name: "unsupported type", def: Definition{Name: "c", Version: "v1", Fields: map[string]string{"a": "float"}}, wantErr: `field a: unsupported type "float"`},
		{name: "double pointer", def: Definition{Name: "c", Version: "v1", Fields: map[string]string{"a": "**string"}}, wantErr: `field a: unsupported type "**string"`},
		{name: "unsupported optional type", def: Definition{Name: "c", Version: "v1", Fields: map[string]string{"a": "bool"}, Optional: map[string]string{"b": "[]string"}}, wantErr: `field b: unsupported type "[]string"`},
		{name: "required and optional", def: Definition{Name: "c", Version: "v1", Fields: map[string]string{"a": "bool"}, Optional: map[string]string{"a": "bool"}}, wantErr: "field a is both required and optional"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.def.Validate()
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("got error %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestFieldTypeMatches(t *testing.T) {
	str := types.Typ[types.String]
	for _, tc := range []struct {
		name string
		got  types.Type
		want string
		ok   bool
	}{
		{name: "exact string", got: str, want: "string", ok: true},
		{name: "pointer needs nullable type", got: types.NewPointer(str), want: "string"},
		{name: "nullable accepts pointer", got: types.NewPointer(str), want: "*string", ok: true},
		{name: "nullable accepts value", got: str, want: "*string", ok: true},
		{name: "nullable checks the base type", got: types.NewPointer(types.Typ[types.Bool]), want: "*string"},
		{name: "int is int64", got: types.Typ[types.Int64], want: "int", ok: true},
		{name: "int32", got: types.Typ[types.Int32], want: "int32", ok: true},
		{name: "int32 is not int", got: types.Typ[types.Int32], want: "int"},
		{name: "int is not int32", got: types.Typ[types.Int64], want: "int32"},
		{name: "Go int is neither", got: types.Typ[types.Int], want: "int32"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := matches(tc.got, tc.want); got != tc.ok {
				t.Fatalf("matches(%s, %s) = %v, want %v", tc.got, tc.want, got, tc.ok)
			}
		})
	}
}

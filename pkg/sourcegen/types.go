// Package sourcegen extracts declared architecture mappings and checked Go
// structure. It does not infer behavior or discover runtime controller wiring.
package sourcegen

import (
	"crypto/sha256"
	"fmt"
	"generative-architecture/pkg/contracts"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type Config struct {
	Contracts  []string `yaml:"contracts,omitempty"`
	Mappings   string   `yaml:"mappings"`
	Packages   []string `yaml:"packages"`
	Descriptor string   `yaml:"descriptor"`
}
type Location struct {
	File string `yaml:"file"`
	Line int    `yaml:"line"`
}
type Field struct {
	GoPath   string `yaml:"goPath"`
	JSONPath string `yaml:"jsonPath"`
	GoType   string `yaml:"goType"`
}
type Resource struct {
	ID      string   `yaml:"id"`
	Group   string   `yaml:"group"`
	Version string   `yaml:"version"`
	Kind    string   `yaml:"kind"`
	Fields  []Field  `yaml:"fields"`
	Source  Location `yaml:"source"`
}
type Port struct {
	Name      string `yaml:"name"`
	Direction string `yaml:"direction,omitempty"`
	Resource  string `yaml:"resource"`
	GoField   string `yaml:"goField"`
	JSONPath  string `yaml:"jsonPath"`
	GoType    string `yaml:"goType"`
	Type      string `yaml:"type"`
	Protocol  string `yaml:"protocol,omitempty"`
	Key       string `yaml:"key,omitempty"`
	Evidence  string `yaml:"evidence"`
}
type Action struct {
	Name      string   `yaml:"name"`
	Method    string   `yaml:"method"`
	Signature string   `yaml:"signature"`
	Source    Location `yaml:"source"`
}
type Controller struct {
	Component string   `yaml:"component"`
	GoType    string   `yaml:"goType"`
	Contract  string   `yaml:"contract,omitempty"`
	Version   string   `yaml:"version,omitempty"`
	Primary   string   `yaml:"primary"`
	Owns      string   `yaml:"owns"`
	Source    Location `yaml:"source"`
	Ports     []Port   `yaml:"ports"`
	Actions   []Action `yaml:"actions"`
}
type Descriptor struct {
	Contracts               []string                     `yaml:"contracts,omitempty"`
	ContractImplementations []contracts.Evidence         `yaml:"contractImplementations,omitempty"`
	Records                 map[string]map[string]string `yaml:"records,omitempty"`
	Mappings                string                       `yaml:"mappings"`
	Generator               string                       `yaml:"generator"`
	Packages                []string                     `yaml:"packages"`
	SourceFiles             map[string]string            `yaml:"sourceFiles"`
	Directories             []string                     `yaml:"directories"`
	Resources               []Resource                   `yaml:"resources"`
	Controllers             []Controller                 `yaml:"controllers"`
}

func Hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func LocalPath(root, path string) (string, error) {
	clean := filepath.Clean(path)
	if path == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must be project-relative: %q", path)
	}
	return filepath.Join(root, clean), nil
}
func inventory(root string, dirs []string, mappings string, contractFiles ...string) (map[string]string, error) {
	paths := append([]string{"go.mod", "go.sum", mappings}, contractFiles...)
	for _, dir := range dirs {
		abs, err := LocalPath(root, dir)
		if err != nil {
			return nil, err
		}
		files, err := filepath.Glob(filepath.Join(abs, "*.go"))
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			rel, err := filepath.Rel(root, file)
			if err != nil {
				return nil, err
			}
			paths = append(paths, filepath.ToSlash(rel))
		}
	}
	sort.Strings(paths)
	out := map[string]string{}
	for _, path := range paths {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, err
		}
		out[path] = Hash(b)
	}
	return out, nil
}

// Verify rejects modified, added or removed source files in the selected packages.
func (d Descriptor) Verify(root string, c Config) error {
	if !reflect.DeepEqual(d.Contracts, c.Contracts) || d.Mappings != c.Mappings || !reflect.DeepEqual(d.Packages, c.Packages) || len(d.Controllers) == 0 || len(d.Directories) == 0 {
		return fmt.Errorf("source descriptor configuration mismatch; run arch extract")
	}
	files, err := inventory(root, d.Directories, c.Mappings, c.Contracts...)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(files, d.SourceFiles) {
		return fmt.Errorf("Go source descriptor is stale; run arch extract (or arch generate)")
	}
	return nil
}

// Mappings contains only the semantic joins that cannot be inferred from types.
type Mappings struct {
	Controllers map[string]ControllerMapping `yaml:"controllers"`
}
type ControllerMapping struct {
	Primary string            `yaml:"primary"`
	Owns    string            `yaml:"owns"`
	Ports   map[string]string `yaml:"ports"`
}

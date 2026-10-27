// Package contracts validates explicit structural contract implementations in
// owned or upstream Go types. Structural compatibility is not behavioural proof.
package contracts

import (
	"fmt"
	"generative-architecture/pkg/goflow"
	"generative-architecture/pkg/krm"
	"go/ast"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type Definition struct {
	Name    string            `yaml:"-"`
	Version string            `yaml:"version"`
	Fields  map[string]string `yaml:"fields"`
}
type Implementation struct {
	Contract string            `yaml:"contract"`
	Package  string            `yaml:"package"`
	Type     string            `yaml:"type"`
	Fields   map[string]string `yaml:"fields,omitempty"`
}
type Evidence struct {
	Contract     string            `yaml:"contract"`
	GoType       string            `yaml:"goType"`
	Module       string            `yaml:"module,omitempty"`
	Version      string            `yaml:"moduleVersion,omitempty"`
	JSONPaths    map[string]string `yaml:"jsonPaths"`
	SourceHashes map[string]string `yaml:"sourceHashes"`
	Origin       string            `yaml:"origin"`
}

func Load(path string) (Definition, error) {
	var d Definition
	meta, err := krm.Read(path, "InteractionContract", &d)
	d.Name = meta.Name
	return d, err
}
func primitive(t types.Type) string {
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return ""
	}
	switch b.Kind() {
	case types.String:
		return "string"
	case types.Bool:
		return "bool"
	case types.Int64:
		return "int"
	}
	return ""
}
func deref(t types.Type) types.Type {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}
func field(t types.Type, path string) (types.Type, string, error) {
	jsonPath := []string{}
	for _, part := range strings.Split(path, ".") {
		st, ok := deref(t).Underlying().(*types.Struct)
		if !ok {
			return nil, "", fmt.Errorf("%s traverses a non-struct", path)
		}
		found := false
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			tag := strings.Split(reflect.StructTag(st.Tag(i)).Get("json"), ",")[0]
			if f.Name() != part && tag != part {
				continue
			}
			if !f.Exported() || tag == "" || tag == "-" {
				return nil, "", fmt.Errorf("contract field requires an exported JSON field")
			}
			found = true
			t = f.Type()
			jsonPath = append(jsonPath, tag)
			break
		}
		if !found {
			return nil, "", fmt.Errorf("missing contract field %s", path)
		}
	}
	return t, strings.Join(jsonPath, "."), nil
}

// Inspect discovers +arch:implements markers in selected packages and/or checks
// external selectors for unmodified upstream types. Contracts use name/version.
func Inspect(root string, definitions []Definition, patterns []string, external []Implementation) ([]Evidence, error) {
	defs := map[string]Definition{}
	for _, d := range definitions {
		ref := d.Name + "/" + d.Version
		if d.Name == "" || d.Version == "" || len(d.Fields) == 0 || defs[ref].Name != "" {
			return nil, fmt.Errorf("invalid/duplicate interaction contract")
		}
		for _, t := range d.Fields {
			if t != "string" && t != "bool" && t != "int" {
				return nil, fmt.Errorf("unsupported contract field type %s", t)
			}
		}
		defs[ref] = d
	}
	seenPatterns := map[string]bool{}
	all := []string{}
	for _, p := range append(append([]string{}, patterns...), packageNames(external)...) {
		if !seenPatterns[p] {
			seenPatterns[p] = true
			all = append(all, p)
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("packages or external selectors required")
	}
	// GOPROXY=off keeps selectors offline: a module missing from the module
	// cache fails instead of being fetched (EXT-5).
	pkgs, err := packages.Load(&packages.Config{Dir: root, Env: append(os.Environ(), "GOPROXY=off"), Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedModule}, all...)
	if err != nil {
		return nil, err
	}
	byName := map[string]*packages.Package{}
	implementations := append([]Implementation(nil), external...)
	origins := map[string]string{}
	locations := map[string]string{}
	selected := map[string]bool{}
	for _, x := range external {
		selected[x.Package] = true
	}
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			if selected[p.PkgPath] && p.Module == nil {
				return nil, fmt.Errorf("external selector package %s: not resolvable from the module graph or module cache; add its module to go.mod (selectors never fetch): %s", p.PkgPath, p.Errors[0])
			}
			return nil, fmt.Errorf("typecheck %s: %s", p.PkgPath, p.Errors[0])
		}
		byName[p.PkgPath] = p
		for _, f := range p.Syntax {
			for _, decl := range f.Decls {
				g, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range g.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					doc := ts.Doc
					if doc == nil && len(g.Specs) == 1 {
						doc = g.Doc
					}
					if doc == nil {
						continue
					}
					for _, comment := range doc.List {
						raw := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
						if ref, ok := strings.CutPrefix(raw, "+arch:implements="); ok {
							impl := Implementation{Contract: ref, Package: p.PkgPath, Type: ts.Name.Name}
							implementations = append(implementations, impl)
							origins[p.PkgPath+"."+ts.Name.Name+":"+ref] = "marker"
							locations[p.PkgPath+"."+ts.Name.Name+":"+ref] = relPosition(root, p.Fset.Position(comment.Pos()))
						}
					}
				}
			}
		}
	}
	// Local package selectors resolve by import path, package name, or directory.
	for _, impl := range external {
		if byName[impl.Package] != nil {
			continue
		}
		for _, p := range pkgs {
			if len(p.GoFiles) > 0 {
				dir, _ := filepath.Abs(filepath.Join(root, impl.Package))
				if filepath.Dir(p.GoFiles[0]) == dir {
					byName[impl.Package] = p
				}
			}
		}
	}
	out := []Evidence{}
	seen := map[string]bool{}
	for i, impl := range implementations {
		// Diagnostics name the marker position or the external selector.
		where := fmt.Sprintf("external selector %s.%s for %s", impl.Package, impl.Type, impl.Contract)
		if i >= len(external) {
			where = locations[impl.Package+"."+impl.Type+":"+impl.Contract] + ": +arch:implements=" + impl.Contract
		}
		d, ok := defs[impl.Contract]
		if !ok {
			return nil, fmt.Errorf("%s: unknown contract %s", where, impl.Contract)
		}
		p := byName[impl.Package]
		if p == nil {
			return nil, fmt.Errorf("%s: unknown selected package %s", where, impl.Package)
		}
		object := p.Types.Scope().Lookup(impl.Type)
		if object == nil {
			return nil, fmt.Errorf("%s: type %s not found in %s", where, impl.Type, p.PkgPath)
		}
		if _, ok := object.(*types.TypeName); !ok {
			return nil, fmt.Errorf("%s: selector is not a type", where)
		}
		id := p.PkgPath + "." + impl.Type + ":" + impl.Contract
		if seen[id] {
			return nil, fmt.Errorf("%s: duplicate contract implementation %s", where, id)
		}
		seen[id] = true
		evidence := Evidence{Contract: impl.Contract, GoType: p.PkgPath + "." + impl.Type, JSONPaths: map[string]string{}, SourceHashes: map[string]string{}, Origin: origins[id]}
		if evidence.Origin == "" {
			evidence.Origin = "external-selector"
		}
		if p.Module != nil {
			evidence.Module = p.Module.Path
			evidence.Version = p.Module.Version
		}
		shapes := []string{}
		for logical, want := range d.Fields {
			path := impl.Fields[logical]
			if path == "" {
				path = logical
			}
			t, jsonPath, err := field(object.Type(), path)
			if err != nil {
				return nil, fmt.Errorf("%s: field %s: %w", where, logical, err)
			}
			if got := primitive(t); got != want {
				return nil, fmt.Errorf("%s: field %s: expected %s, got %s", where, logical, want, t)
			}
			evidence.JSONPaths[logical] = jsonPath
			shapes = append(shapes, logical+":"+jsonPath+":"+types.TypeString(t, nil))
		}
		for logical := range impl.Fields {
			if _, ok := d.Fields[logical]; !ok {
				return nil, fmt.Errorf("%s: unknown mapped contract field %s", where, logical)
			}
		}
		// Hash the resolved contract paths, including fields of imported structs.
		sort.Strings(shapes)
		evidence.SourceHashes["type-shape"] = goflow.Hash([]byte(types.TypeString(object.Type().Underlying(), nil) + "\n" + strings.Join(shapes, "\n")))
		for _, path := range p.GoFiles {
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			key, err := sourceKey(root, p.Module, path)
			if err != nil {
				return nil, err
			}
			evidence.SourceHashes[key] = goflow.Hash(b)
		}
		out = append(out, evidence)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no contract implementations selected")
	}
	return out, nil
}
func packageNames(xs []Implementation) []string {
	out := []string{}
	for _, x := range xs {
		out = append(out, x.Package)
	}
	return out
}

// relPosition reports a source position relative to root, for diagnostics.
func relPosition(root string, pos token.Position) string {
	if rel, err := filepath.Rel(root, pos.Filename); err == nil && !strings.HasPrefix(rel, "..") {
		pos.Filename = filepath.ToSlash(rel)
	}
	return pos.String()
}

// sourceKey names a hashed source file. A file of a dependency module is
// named module@version/path, so the key does not depend on the location of
// the module cache. A file of the main module is named relative to root.
func sourceKey(root string, m *packages.Module, path string) (string, error) {
	if m != nil && !m.Main {
		dir, version := m.Dir, m.Version
		if m.Replace != nil {
			dir = m.Replace.Dir
			if m.Replace.Version != "" {
				version = m.Replace.Version
			}
		}
		if rel, err := filepath.Rel(dir, path); err == nil && dir != "" && !strings.HasPrefix(rel, "..") {
			return m.Path + "@" + version + "/" + filepath.ToSlash(rel), nil
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

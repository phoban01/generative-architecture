package sourcegen

import (
	"fmt"
	"github.com/phoban01/generative-architecture/pkg/contracts"
	"github.com/phoban01/generative-architecture/pkg/krm"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"sigs.k8s.io/controller-tools/pkg/markers"
)

type markedType struct {
	pkg    *packages.Package
	node   *ast.TypeSpec
	typ    *types.Named
	marks  map[string][]any
	source Location
}

// kubebuilderRoot is the kubebuilder marker that declares a root API object.
// A type carrying it is a resource without needing +arch:resource (ADOPT-2);
// list types, which carry it too, are not resources.
const kubebuilderRoot = "kubebuilder:object:root"

func definitions() (map[string]*markers.Definition, error) {
	out := map[string]*markers.Definition{}
	for name, value := range map[string]any{"arch:resource": struct{}{}, "arch:controller": "", "arch:action": "", "arch:implements": "", kubebuilderRoot: false} {
		def, err := markers.MakeDefinition(name, markers.DescribesType, value)
		if err != nil {
			return nil, err
		}
		out[name] = def
	}
	return out, nil
}
func parseComments(group *ast.CommentGroup, defs map[string]*markers.Definition, consumed map[*ast.Comment]bool) (map[string][]any, error) {
	out := map[string][]any{}
	if group == nil {
		return out, nil
	}
	for _, c := range group.List {
		raw := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		if !strings.HasPrefix(raw, "+arch:") && raw != "+"+kubebuilderRoot && !strings.HasPrefix(raw, "+"+kubebuilderRoot+"=") {
			continue
		}
		var name string
		for candidate := range defs {
			if raw == "+"+candidate || strings.HasPrefix(raw, "+"+candidate+"=") || strings.HasPrefix(raw, "+"+candidate+":") {
				name = candidate
				break
			}
		}
		if name == "" {
			return nil, fmt.Errorf("unknown architecture marker %q", raw)
		}
		value, err := defs[name].Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", raw, err)
		}
		out[name] = append(out[name], value)
		consumed[c] = true
	}
	return out, nil
}
func only[T any](m markedType, name string) (T, error) {
	var zero T
	values := m.marks[name]
	if len(values) != 1 {
		return zero, fmt.Errorf("%s: expected exactly one %s marker", m.typ.Obj().Name(), name)
	}
	return values[0].(T), nil
}
func qualified(t *types.Named) string { return t.Obj().Pkg().Path() + "." + t.Obj().Name() }
func location(root string, p *packages.Package, pos token.Pos) Location {
	loc := p.Fset.Position(pos)
	rel, _ := filepath.Rel(root, loc.Filename)
	return Location{filepath.ToSlash(rel), loc.Line}
}
func named(t types.Type) *types.Named { n, _ := types.Unalias(t).(*types.Named); return n }

// listType reports whether t is a Kubernetes list object: a struct embedding
// metav1.ListMeta. Lists carry +kubebuilder:object:root=true but are not
// resources a controller reconciles or a catalog lists.
func listType(t types.Type) bool {
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := 0; i < st.NumFields(); i++ {
		if f := st.Field(i); f.Embedded() && types.TypeString(f.Type(), nil) == "k8s.io/apimachinery/pkg/apis/meta/v1.ListMeta" {
			return true
		}
	}
	return false
}

// resourceOptIn folds the kubebuilder root marker into +arch:resource: a
// type with either marker is a resource, with both it is one resource, and
// a list type or root=false adds nothing.
func resourceOptIn(marks map[string][]any, t types.Type) {
	roots := marks[kubebuilderRoot]
	delete(marks, kubebuilderRoot)
	root := false
	for _, v := range roots {
		root = root || v.(bool)
	}
	if root && !listType(t) && len(marks["arch:resource"]) == 0 {
		marks["arch:resource"] = []any{struct{}{}}
	}
}
func field(t types.Type, path string) (types.Type, string, error) {
	var jsonParts []string
	for _, part := range strings.Split(path, ".") {
		st, ok := t.Underlying().(*types.Struct)
		if !ok {
			return nil, "", fmt.Errorf("%s traverses non-struct %s", path, t)
		}
		found := false
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if f.Name() != part {
				continue
			}
			found = true
			tag := strings.Split(reflect.StructTag(st.Tag(i)).Get("json"), ",")[0]
			if !f.Exported() || tag == "" || tag == "-" {
				return nil, "", fmt.Errorf("%s must name exported fields with explicit JSON names", path)
			}
			jsonParts = append(jsonParts, tag)
			t = f.Type()
			break
		}
		if !found {
			return nil, "", fmt.Errorf("Go field %s does not exist", path)
		}
	}
	return t, strings.Join(jsonParts, "."), nil
}
func valueType(t types.Type, records map[string]map[string]string) (string, error) {
	switch v := t.Underlying().(type) {
	case *types.Basic:
		switch v.Kind() {
		case types.String:
			return "string", nil
		case types.Bool:
			return "bool", nil
		case types.Int64:
			return "int", nil
		}
	case *types.Struct:
		// Infer flat named records from JSON fields, retaining their checked shape.
		shape := map[string]string{}
		for i := 0; i < v.NumFields(); i++ {
			f := v.Field(i)
			b, ok := f.Type().Underlying().(*types.Basic)
			if !ok {
				return "", fmt.Errorf("unsupported nested record %s", t)
			}
			tag := strings.Split(reflect.StructTag(v.Tag(i)).Get("json"), ",")[0]
			if !f.Exported() || tag == "" || tag == "-" || shape[tag] != "" {
				return "", fmt.Errorf("unsupported record field %s", f.Name())
			}
			switch b.Kind() {
			case types.String:
				shape[tag] = "string"
			case types.Bool:
				shape[tag] = "bool"
			case types.Int64:
				shape[tag] = "int"
			default:
				return "", fmt.Errorf("unsupported record field type %s", b)
			}
		}
		named, ok := types.Unalias(t).(*types.Named)
		if !ok || len(shape) == 0 {
			return "", fmt.Errorf("record must be a nonempty named type: %s", t)
		}
		name := named.Obj().Name()
		if old, exists := records[name]; exists && !reflect.DeepEqual(old, shape) {
			return "", fmt.Errorf("ambiguous record name %s", name)
		}
		records[name] = shape
		return name, nil
	}
	return "", fmt.Errorf("unsupported model value type %s", t)
}
func fields(t *types.Named) []Field {
	var result []Field
	var visit func(types.Type, string, string, int)
	visit = func(t types.Type, gp, jp string, depth int) {
		st, ok := t.Underlying().(*types.Struct)
		if !ok || depth > 8 {
			return
		}
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			tag := strings.Split(reflect.StructTag(st.Tag(i)).Get("json"), ",")[0]
			if !f.Exported() || tag == "" || tag == "-" || f.Embedded() {
				continue
			}
			goPath, jsonPath := gp+f.Name(), jp+tag
			result = append(result, Field{goPath, jsonPath, types.TypeString(f.Type(), nil)})
			visit(f.Type(), goPath+".", jsonPath+".", depth+1)
		}
	}
	visit(t, "", "", 0)
	return result
}

func reconcileSignature(t types.Type) bool {
	s, ok := t.(*types.Signature)
	if !ok || s.Variadic() || s.Params().Len() != 2 || s.Results().Len() != 2 {
		return false
	}
	return types.TypeString(s.Params().At(0).Type(), nil) == "context.Context" && types.TypeString(s.Params().At(1).Type(), nil) == "sigs.k8s.io/controller-runtime/pkg/reconcile.Request" && types.TypeString(s.Results().At(0).Type(), nil) == "sigs.k8s.io/controller-runtime/pkg/reconcile.Result" && types.TypeString(s.Results().At(1).Type(), nil) == "error"
}

func packageGV(p *packages.Package) (string, string, error) {
	group, version := "", p.Name
	for _, file := range p.Syntax {
		if file.Doc == nil {
			continue
		}
		for _, c := range file.Doc.List {
			raw := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if v, ok := strings.CutPrefix(raw, "+groupName="); ok {
				if group != "" && group != v {
					return "", "", fmt.Errorf("conflicting groupName markers")
				}
				group = v
			}
			if v, ok := strings.CutPrefix(raw, "+versionName="); ok {
				if version != p.Name && version != v {
					return "", "", fmt.Errorf("conflicting versionName markers")
				}
				version = v
			}
		}
	}
	if group == "" || version == "" {
		return "", "", fmt.Errorf("resource package %s needs a package-level +groupName marker", p.PkgPath)
	}
	return group, version, nil
}
func loadMappings(root string, c Config) (Mappings, error) {
	var mappings Mappings
	path, err := LocalPath(root, c.Mappings)
	if err != nil {
		return mappings, err
	}
	_, err = krm.Read(path, "SourceBindings", &mappings)
	return mappings, err
}
func Extract(root string, c Config) (*Descriptor, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = abs
	if len(c.Packages) == 0 {
		return nil, fmt.Errorf("source packages required")
	}
	for _, pattern := range c.Packages {
		if !strings.HasPrefix(pattern, "./") || strings.Contains(pattern, "..") {
			return nil, fmt.Errorf("use explicit local package paths, got %q", pattern)
		}
	}
	mappings, err := loadMappings(root, c)
	if err != nil {
		return nil, err
	}
	pkgs, err := packages.Load(&packages.Config{Dir: root, Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}, c.Packages...)
	if err != nil {
		return nil, err
	}
	defs, err := definitions()
	if err != nil {
		return nil, err
	}
	d := &Descriptor{Records: map[string]map[string]string{}, Generator: "arch sourcegen/v3 (controller-tools markers + go/types)", Packages: append([]string(nil), c.Packages...), Mappings: c.Mappings, Contracts: append([]string(nil), c.Contracts...)}
	var marked []markedType
	dirs := map[string]bool{}
	actions := map[string][]Action{}
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("load %s: %s", p.PkgPath, p.Errors[0])
		}
		for _, file := range p.GoFiles {
			rel, err := filepath.Rel(root, filepath.Dir(file))
			if err != nil || strings.HasPrefix(rel, "..") {
				return nil, fmt.Errorf("source package outside project")
			}
			dirs[filepath.ToSlash(rel)] = true
		}
		consumed := map[*ast.Comment]bool{}
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok {
					marks, err := parseComments(fd.Doc, defs, consumed)
					if err != nil {
						return nil, err
					}
					delete(marks, kubebuilderRoot)
					if len(marks) == 0 {
						continue
					}
					if len(marks) != 1 || len(marks["arch:action"]) != 1 || fd.Recv == nil {
						return nil, fmt.Errorf("method declarations accept one +arch:action marker")
					}
					fn := p.TypesInfo.Defs[fd.Name].(*types.Func)
					sig := fn.Type().(*types.Signature)
					recv := sig.Recv().Type()
					if ptr, ok := recv.(*types.Pointer); ok {
						recv = ptr.Elem()
					}
					n := named(recv)
					if n == nil {
						return nil, fmt.Errorf("action receiver must be a named controller")
					}
					actions[qualified(n)] = append(actions[qualified(n)], Action{marks["arch:action"][0].(string), fd.Name.Name, types.TypeString(fn.Type(), nil), location(root, p, fd.Pos())})
					continue
				}
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts := spec.(*ast.TypeSpec)
					doc := ts.Doc
					if doc == nil && len(gd.Specs) == 1 {
						doc = gd.Doc
					}
					marks, err := parseComments(doc, defs, consumed)
					if err != nil {
						return nil, fmt.Errorf("%s: %w", p.Fset.Position(ts.Pos()), err)
					}
					if len(marks["arch:implements"]) > 0 {
						if len(c.Contracts) == 0 {
							return nil, fmt.Errorf("+arch:implements requires source contract definitions")
						}
						delete(marks, "arch:implements")
					}
					obj := p.TypesInfo.Defs[ts.Name]
					if obj == nil {
						return nil, fmt.Errorf("%s: type %s has no type information", p.Fset.Position(ts.Pos()), ts.Name.Name)
					}
					resourceOptIn(marks, obj.Type())
					if len(marks) == 0 {
						continue
					}
					if len(marks) != 1 || len(marks["arch:action"]) > 0 {
						return nil, fmt.Errorf("type declarations accept one resource or controller marker")
					}
					n := named(obj.Type())
					if n == nil || ts.Assign.IsValid() {
						return nil, fmt.Errorf("markers require a defined Go type")
					}
					marked = append(marked, markedType{p, ts, n, marks, location(root, p, ts.Pos())})
				}
			}
			for _, group := range file.Comments {
				for _, comment := range group.List {
					if strings.Contains(comment.Text, "+arch:") && !consumed[comment] {
						return nil, fmt.Errorf("%s: misplaced architecture marker", p.Fset.Position(comment.Pos()))
					}
				}
			}
		}
	}
	if len(c.Contracts) > 0 {
		definitions := []contracts.Definition{}
		for _, path := range c.Contracts {
			full, err := LocalPath(root, path)
			if err != nil {
				return nil, err
			}
			definition, err := contracts.Load(full)
			if err != nil {
				return nil, err
			}
			definitions = append(definitions, definition)
		}
		evidence, err := contracts.Inspect(root, definitions, c.Packages, nil)
		if err != nil {
			return nil, err
		}
		d.ContractImplementations = evidence
	}
	for dir := range dirs {
		d.Directories = append(d.Directories, dir)
	}
	sort.Strings(d.Directories)
	resources := map[string]*types.Named{}
	resourceIDs := map[string]string{}
	gvks := map[string]bool{}
	for _, m := range marked {
		if len(m.marks["arch:resource"]) == 0 {
			continue
		}
		if _, err := only[struct{}](m, "arch:resource"); err != nil {
			return nil, err
		}
		if _, ok := m.typ.Underlying().(*types.Struct); !ok {
			return nil, fmt.Errorf("resource must be a struct")
		}
		group, version, err := packageGV(m.pkg)
		if err != nil {
			return nil, err
		}
		id, kind := qualified(m.typ), m.typ.Obj().Name()
		gvk := group + "/" + version + "/" + kind
		if _, ok := resources[kind]; ok || gvks[gvk] {
			return nil, fmt.Errorf("ambiguous resource name or GVK: %s", kind)
		}
		resources[kind] = m.typ
		resourceIDs[kind] = id
		gvks[gvk] = true
		d.Resources = append(d.Resources, Resource{id, group, version, kind, fields(m.typ), m.source})
	}
	components := map[string]bool{}
	for _, m := range marked {
		if len(m.marks["arch:resource"]) > 0 {
			continue
		}
		component, err := only[string](m, "arch:controller")
		if err != nil {
			return nil, err
		}
		if component == "" || components[component] {
			return nil, fmt.Errorf("duplicate/empty component %s", component)
		}
		components[component] = true
		mapping, ok := mappings.Controllers[component]
		if !ok {
			return nil, fmt.Errorf("missing source mapping for %s", component)
		}
		if resources[mapping.Primary] == nil || (mapping.Owns != "" && resources[mapping.Owns] == nil) {
			return nil, fmt.Errorf("controller %s references unknown primary/owned resource", component)
		}
		method, _, _ := types.LookupFieldOrMethod(types.NewPointer(m.typ), true, m.pkg.Types, "Reconcile")
		if method == nil || !reconcileSignature(method.Type()) {
			return nil, fmt.Errorf("%s must implement Reconcile(context.Context, reconcile.Request) (reconcile.Result, error)", qualified(m.typ))
		}
		ctrl := Controller{Component: component, GoType: qualified(m.typ), Primary: resourceIDs[mapping.Primary], Owns: resourceIDs[mapping.Owns], Source: m.source}
		for name, selector := range mapping.Ports {
			resource, goField, ok := strings.Cut(selector, ".")
			if !ok || name == "" {
				return nil, fmt.Errorf("port %s requires Resource.Go.Field selector", name)
			}
			r := resources[resource]
			if r == nil {
				return nil, fmt.Errorf("unknown port resource %s", resource)
			}
			goType, jsonPath, modelType := "object existence", "$exists", "bool"
			if goField != "$exists" {
				t, j, err := field(r, goField)
				if err != nil {
					return nil, fmt.Errorf("%s.%s: %w", component, name, err)
				}
				mt, err := valueType(t, d.Records)
				if err != nil {
					return nil, err
				}
				goType, jsonPath, modelType = types.TypeString(t, nil), j, mt
			}
			ctrl.Ports = append(ctrl.Ports, Port{Name: name, Resource: resourceIDs[resource], GoField: goField, JSONPath: jsonPath, GoType: goType, Type: "map(string, " + modelType + ")", Evidence: "semantic join declared in YAML; field path and type checked from Go"})
		}
		seen := map[string]bool{}
		for _, a := range actions[qualified(m.typ)] {
			if a.Name == "" || seen[a.Name] {
				return nil, fmt.Errorf("duplicate/empty action %s", a.Name)
			}
			seen[a.Name] = true
			ctrl.Actions = append(ctrl.Actions, a)
		}
		delete(actions, qualified(m.typ))
		sort.Slice(ctrl.Ports, func(i, j int) bool { return ctrl.Ports[i].Name < ctrl.Ports[j].Name })
		sort.Slice(ctrl.Actions, func(i, j int) bool { return ctrl.Actions[i].Name < ctrl.Actions[j].Name })
		d.Controllers = append(d.Controllers, ctrl)
	}
	if len(actions) > 0 {
		return nil, fmt.Errorf("action marker on a type without +arch:controller")
	}
	for id := range mappings.Controllers {
		if !components[id] {
			return nil, fmt.Errorf("source mapping %s has no annotated controller", id)
		}
	}
	sort.Slice(d.Resources, func(i, j int) bool { return d.Resources[i].ID < d.Resources[j].ID })
	sort.Slice(d.Controllers, func(i, j int) bool { return d.Controllers[i].Component < d.Controllers[j].Component })
	if len(d.Controllers) == 0 {
		return nil, fmt.Errorf("no architecture controllers found")
	}
	d.SourceFiles, err = inventory(root, d.Directories, c.Mappings, c.Contracts...)
	return d, err
}

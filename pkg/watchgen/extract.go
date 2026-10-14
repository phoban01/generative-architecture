// Package watchgen extracts a deliberately restricted controller-runtime builder
// setup. Unknown predicates, options, handlers and setup control flow fail closed.
package watchgen

import (
	"fmt"
	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/goflow"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

type Rule struct {
	Resource      effect.Resource `yaml:"resource"`
	GoType        string          `yaml:"goType"`
	Unstructured  bool            `yaml:"unstructured,omitempty"`
	Mapping       string          `yaml:"mapping"` // self, owner, or a selected Go mapping function
	Line          int             `yaml:"line"`
	NamePath      []string        `yaml:"namePath,omitempty"`
	Predicates    []string        `yaml:"predicates,omitempty"`
	MappingSHA256 string          `yaml:"mappingSha256,omitempty"`
	// Owner mappings enqueue the owner reference of the given kind. The
	// owner resource is kept so IR records carry its full GVK.
	OwnerKind      string           `yaml:"ownerKind,omitempty"`
	Owner          *effect.Resource `yaml:"owner,omitempty"`
	OnlyController bool             `yaml:"onlyController,omitempty"`
}
type Result struct {
	Name         string `yaml:"name,omitempty"` // controller name given by Named, if any
	Rules        []Rule `yaml:"rules"`
	SourceSHA256 string `yaml:"sourceSha256"`
	MethodSHA256 string `yaml:"methodSha256"`
}

// Extract reads a single fluent builder return and supported local field-reference
// mappers. Resources supplies resolved Go import-path.Type names and checked GVKs.
// Callers typecheck the input package; this frontend checks the supported syntax.
func Extract(source []byte, receiver, method string, resources map[string]effect.Resource) (Result, error) {
	return ExtractWithGVKs(source, receiver, method, resources, nil)
}

// ExtractWithGVKs also resolves unstructured watch objects. The optional bindings
// map gives exact GVKs for runtime expressions such as r.InfraGVK; unresolved
// discovery values fail rather than silently dropping a watch from the model.
func ExtractWithGVKs(source []byte, receiver, method string, resources, bindings map[string]effect.Resource) (Result, error) {
	out := Result{SourceSHA256: goflow.Hash(source)}
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "wiring.go", source, parser.ParseComments)
	if err != nil {
		return out, err
	}
	imports := map[string]string{}
	for _, i := range f.Imports {
		path, err := strconv.Unquote(i.Path.Value)
		if err != nil {
			return out, err
		}
		if i.Name == nil {
			defaults := map[string]string{"context": "context", "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured": "unstructured", "k8s.io/apimachinery/pkg/runtime/schema": "schema", "sigs.k8s.io/controller-runtime": "controllerruntime", "sigs.k8s.io/controller-runtime/pkg/client": "client", "sigs.k8s.io/controller-runtime/pkg/handler": "handler", "sigs.k8s.io/controller-runtime/pkg/reconcile": "reconcile", "sigs.k8s.io/controller-runtime/pkg/builder": "builder", "sigs.k8s.io/controller-runtime/pkg/predicate": "predicate"}
			name, ok := defaults[path]
			if !ok {
				return out, diag(fs, i, "import", "supply an explicit import alias for package "+path)
			}
			imports[name] = path
		} else {
			imports[i.Name.Name] = path
		}
	}
	fn := goflow.FindMethod(f, receiver, method)
	if fn == nil || fn.Body == nil || len(fn.Body.List) == 0 {
		return out, diag(fs, f.Name, "method", fmt.Sprintf("expected a builder return in %s.%s", receiver, method))
	}
	ret, ok := fn.Body.List[len(fn.Body.List)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return out, diag(fs, fn.Body.List[len(fn.Body.List)-1], "return", "expected a single builder return")
	}
	out.MethodSHA256 = goflow.Hash([]byte(goflow.Code(fn)))
	qualified := func(e ast.Expr) string {
		s, ok := e.(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		id, ok := s.X.(*ast.Ident)
		if !ok {
			return ""
		}
		return imports[id.Name] + "." + s.Sel.Name
	}
	if fn.Recv == nil || len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 ||
		len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 ||
		qualified(fn.Type.Params.List[0].Type) != "sigs.k8s.io/controller-runtime.Manager" {
		return out, diag(fs, fn.Type, "signature", "expected a named receiver and one controller-runtime Manager parameter")
	}
	receiverName := fn.Recv.List[0].Names[0].Name
	managerName := fn.Type.Params.List[0].Names[0].Name
	isName := func(e ast.Expr, name string) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == name && name != "_"
	}
	objects, err := unstructuredObjects(fs, fn.Body.List[:len(fn.Body.List)-1], qualified, bindings)
	if err != nil {
		return out, err
	}
	usedObjects := map[string]bool{}
	resource := func(e ast.Expr) (string, effect.Resource, error) {
		if id, ok := e.(*ast.Ident); ok {
			if r, ok := objects[id.Name]; ok {
				usedObjects[id.Name] = true
				return "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured.Unstructured", r, nil
			}
			return "", effect.Resource{}, diag(fs, id, "watch-object", "unresolved watch object "+id.Name)
		}
		ptr, ok := e.(*ast.UnaryExpr)
		if !ok || ptr.Op != token.AND {
			return "", effect.Resource{}, diag(fs, e, "watch-object", "expected a pointer to an empty resource literal")
		}
		lit, ok := ptr.X.(*ast.CompositeLit)
		if !ok || len(lit.Elts) != 0 {
			return "", effect.Resource{}, diag(fs, e, "watch-object", "expected an empty resource literal")
		}
		name := qualified(lit.Type)
		r, ok := resources[name]
		if !ok {
			return "", r, diag(fs, e, "resource", "unknown watched resource "+name)
		}
		return name, r, nil
	}
	var visit func(ast.Expr) error
	primary := false
	var primaryResource effect.Resource
	globalPredicates := []string{}
	parsePredicate := func(e ast.Expr) (string, error) {
		lit, ok := e.(*ast.CompositeLit)
		if !ok || len(lit.Elts) != 0 {
			return "", diag(fs, e, "predicate", "unsupported predicate expression")
		}
		name := qualified(lit.Type)
		switch name {
		case "sigs.k8s.io/controller-runtime/pkg/predicate.GenerationChangedPredicate":
			return "generationChanged", nil
		case "sigs.k8s.io/controller-runtime/pkg/predicate.ResourceVersionChangedPredicate":
			return "resourceVersionChanged", nil
		}
		return "", diag(fs, e, "predicate", "unsupported predicate "+name)
	}
	// parseOptions reads per-watch builder options; only WithPredicates is supported.
	parseOptions := func(args []ast.Expr) ([]string, error) {
		predicates := []string{}
		for _, arg := range args {
			opt, ok := arg.(*ast.CallExpr)
			if !ok || qualified(opt.Fun) != "sigs.k8s.io/controller-runtime/pkg/builder.WithPredicates" || len(opt.Args) == 0 || opt.Ellipsis.IsValid() {
				return nil, diag(fs, arg, "option", "unsupported watch option")
			}
			for _, arg := range opt.Args {
				p, err := parsePredicate(arg)
				if err != nil {
					return nil, err
				}
				predicates = append(predicates, p)
			}
		}
		return predicates, nil
	}
	// newRule starts a self-mapped rule for the watched object e at the builder call sel.
	newRule := func(sel *ast.Ident, e ast.Expr, predicates []string) (Rule, error) {
		name, r, err := resource(e)
		if err != nil {
			return Rule{}, err
		}
		return Rule{Resource: r, GoType: name, Unstructured: name == "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured.Unstructured", Mapping: "self", Line: fs.Position(sel.Pos()).Line, NamePath: []string{"Name"}, Predicates: predicates}, nil
	}
	visit = func(e ast.Expr) error {
		call, ok := e.(*ast.CallExpr)
		if !ok || call.Ellipsis.IsValid() {
			return diag(fs, e, "builder", "unsupported watch builder expression")
		}
		if qualified(call.Fun) == "sigs.k8s.io/controller-runtime.NewControllerManagedBy" {
			if len(call.Args) != 1 || !isName(call.Args[0], managerName) {
				return diag(fs, call, "NewControllerManagedBy", "unsupported manager arguments")
			}
			return nil
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return diag(fs, call, "builder", "unsupported builder call")
		}
		if err := visit(sel.X); err != nil {
			return err
		}
		switch sel.Sel.Name {
		case "Complete":
			if len(call.Args) != 1 || e != ret.Results[0] || !isName(call.Args[0], receiverName) {
				return diag(fs, sel.Sel, "Complete", "Complete must terminate setup with its receiver")
			}
		case "Named":
			if len(call.Args) != 1 {
				return diag(fs, sel.Sel, "Named", "Named takes exactly one controller name")
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return diag(fs, call.Args[0], "Named", "controller name must be a string literal")
			}
			name, err := strconv.Unquote(lit.Value)
			if err != nil {
				return err
			}
			if name == "" {
				return diag(fs, call.Args[0], "Named", "controller name must not be empty")
			}
			if out.Name != "" {
				return diag(fs, sel.Sel, "Named", "duplicate controller name")
			}
			out.Name = name
		case "For":
			if len(call.Args) < 1 {
				return diag(fs, sel.Sel, "For", "incomplete watch")
			}
			if primary {
				return diag(fs, sel.Sel, "For", "duplicate primary watch")
			}
			if len(out.Rules) > 0 {
				return diag(fs, sel.Sel, "For", "the primary For watch must precede other watches")
			}
			predicates, err := parseOptions(call.Args[1:])
			if err != nil {
				return err
			}
			rule, err := newRule(sel.Sel, call.Args[0], predicates)
			if err != nil {
				return err
			}
			primary, primaryResource = true, rule.Resource
			out.Rules = append(out.Rules, rule)
		case "Owns":
			if len(call.Args) < 1 {
				return diag(fs, sel.Sel, "Owns", "incomplete watch")
			}
			if !primary {
				return diag(fs, sel.Sel, "Owns", "Owns must follow the primary For watch")
			}
			predicates, err := parseOptions(call.Args[1:])
			if err != nil {
				return err
			}
			rule, err := newRule(sel.Sel, call.Args[0], predicates)
			if err != nil {
				return err
			}
			// Owns enqueues the controlling owner reference of the primary kind.
			owner := primaryResource
			rule.Mapping, rule.NamePath, rule.OwnerKind, rule.Owner, rule.OnlyController = "owner", nil, owner.Kind, &owner, true
			out.Rules = append(out.Rules, rule)
		case "Watches":
			if len(call.Args) < 2 {
				return diag(fs, sel.Sel, "Watches", "incomplete watch")
			}
			predicates, err := parseOptions(call.Args[2:])
			if err != nil {
				return err
			}
			rule, err := newRule(sel.Sel, call.Args[0], predicates)
			if err != nil {
				return err
			}
			h, ok := call.Args[1].(*ast.CallExpr)
			if !ok || qualified(h.Fun) != "sigs.k8s.io/controller-runtime/pkg/handler.EnqueueRequestsFromMapFunc" || len(h.Args) != 1 {
				return diag(fs, call.Args[1], "handler", "unsupported watch handler")
			}
			id, ok := h.Args[0].(*ast.Ident)
			if !ok {
				return diag(fs, h.Args[0], "handler", "mapping must name a local function")
			}
			rule.Mapping = id.Name
			if err := extractMapping(fs, f, imports, &rule); err != nil {
				return err
			}
			out.Rules = append(out.Rules, rule)
		case "WithEventFilter":
			if len(call.Args) != 1 {
				return diag(fs, sel.Sel, "WithEventFilter", "invalid global predicate")
			}
			p, err := parsePredicate(call.Args[0])
			if err != nil {
				return err
			}
			globalPredicates = append(globalPredicates, p)
		default:
			return diag(fs, sel.Sel, sel.Sel.Name, "unsupported builder operation")
		}
		return nil
	}
	outer, ok := ret.Results[0].(*ast.CallExpr)
	if !ok {
		return out, diag(fs, ret, "Complete", "missing Complete")
	}
	sel, ok := outer.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Complete" {
		return out, diag(fs, outer, "Complete", "missing Complete")
	}
	if err = visit(ret.Results[0]); err != nil {
		return out, err
	}
	if len(usedObjects) != len(objects) {
		return out, diag(fs, fn.Body, "watch-object", "unused unstructured watch object")
	}
	if !primary {
		return out, diag(fs, ret, "For", "primary For watch required")
	}
	for i := range out.Rules {
		out.Rules[i].Predicates = append(out.Rules[i].Predicates, globalPredicates...)
	}
	return out, nil
}

// diag reports an unsupported construct at the position of n.
func diag(fs *token.FileSet, n ast.Node, construct, reason string) error {
	return goflow.NewDiagnostic(fs, n, construct, reason)
}

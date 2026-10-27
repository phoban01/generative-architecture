// Package dynamicgen extracts bounded dynamic-client resource dependencies from
// selected Go methods. It does not infer API or helper semantics from call names.
package dynamicgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"generative-architecture/pkg/effect"
	"generative-architecture/pkg/goflow"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Binding instantiates a runtime GVR expression using a checked resource catalog.
// Resources can come from CRDs/discovery without any generated Go API types.
type Binding struct {
	Expression string          `yaml:"expression"`
	Resource   effect.Resource `yaml:"resource"`
	Plural     string          `yaml:"plural"`
	Scope      string          `yaml:"scope"`
}
type Catalog struct {
	Bindings []Binding `yaml:"bindings"`
}

func (b Binding) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: b.Resource.Group, Version: b.Resource.Version, Resource: b.Plural}
}
func (b Binding) GVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: b.Resource.Group, Version: b.Resource.Version, Kind: b.Resource.Kind}
}
func (c Catalog) Resolve(expression string) (Binding, error) {
	for _, b := range c.Bindings {
		if b.Expression == expression {
			return b, nil
		}
	}
	return Binding{}, fmt.Errorf("unresolved dynamic resource %s: supply a bounded catalog binding", expression)
}
func (c Catalog) Mapper() (meta.RESTMapper, error) {
	mapper := meta.NewDefaultRESTMapper(nil)
	expressions := map[string]bool{}
	gvks := map[schema.GroupVersionKind]Binding{}
	gvrs := map[schema.GroupVersionResource]schema.GroupVersionKind{}
	for _, b := range c.Bindings {
		if b.Expression == "" || expressions[b.Expression] || b.Resource.Version == "" || b.Resource.Kind == "" || b.Plural == "" || (b.Scope != "Namespaced" && b.Scope != "Cluster") {
			return nil, fmt.Errorf("invalid/duplicate dynamic resource binding %+v", b)
		}
		expressions[b.Expression] = true
		if prior, ok := gvrs[b.GVR()]; ok && prior != b.GVK() {
			return nil, fmt.Errorf("ambiguous GVR %v", b.GVR())
		}
		gvrs[b.GVR()] = b.GVK()
		if prior, ok := gvks[b.GVK()]; ok {
			if prior.GVR() != b.GVR() || prior.Scope != b.Scope {
				return nil, fmt.Errorf("ambiguous GVK %v", b.GVK())
			}
			continue
		}
		gvks[b.GVK()] = b
		scope := meta.RESTScopeNamespace
		if b.Scope == "Cluster" {
			scope = meta.RESTScopeRoot
		}
		// Explicit plural avoids UnsafeGuessKindToResource and handles irregular APIs.
		mapper.AddSpecific(b.GVK(), b.GVR(), b.GVR(), scope)
	}
	return mapper, nil
}

// dynamicOperations maps each extracted dynamic-client method to the effect
// operation the observer records and the argument count without
// subresources. Get(ctx, name, opts) has three arguments; List(ctx, opts) two.
var dynamicOperations = map[string]struct {
	name string
	args int
}{
	"Get":          {"get", 3},
	"Create":       {"create", 3},
	"Update":       {"update", 3},
	"UpdateStatus": {"status.update", 3},
	"List":         {"list", 2},
	"Delete":       {"delete", 3},
	"Patch":        {"patch", 5},
	"Apply":        {"apply", 4},
	"ApplyStatus":  {"status.apply", 4},
}

type Access struct {
	Method       string  `yaml:"method"`
	Effect       string  `yaml:"effect"`
	Operation    string  `yaml:"operation"`
	Binding      Binding `yaml:"binding"`
	Namespace    string  `yaml:"namespace,omitempty"`
	Line         int     `yaml:"line"`
	MethodSHA256 string  `yaml:"methodSha256"`
}
type Result struct {
	Accesses     []Access `yaml:"accesses"`
	SourceSHA256 string   `yaml:"sourceSha256"`
}

// Extract identifies chained Client.Resource(boundGVR).Namespace(expr).Operation
// calls. Methods maps selected method names to declared effect IDs. An escaped or
// aliased selected client, unresolved GVR, or unsupported operation fails closed.
// Typecheck the input package before using this bounded syntax frontend.
func Extract(source []byte, receiver, clientExpression string, methods map[string]string, catalog Catalog) (Result, error) {
	out := Result{SourceSHA256: goflow.Hash(source)}
	if _, err := catalog.Mapper(); err != nil {
		return out, err
	}
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "dynamic_store.go", source, parser.ParseComments)
	if err != nil {
		return out, err
	}
	names := []string{}
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fn := goflow.FindMethod(f, receiver, name)
		if fn == nil || fn.Body == nil {
			return out, goflow.NewDiagnostic(fs, f.Name, "method", fmt.Sprintf("dynamic method not found: %s.%s", receiver, name))
		}
		roots := map[ast.Expr]bool{}
		var failure error
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name == "Resource" || sel.Sel.Name == "Namespace" {
				return true
			}
			chain, ok := sel.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			var namespace string
			if part, ok := chain.Fun.(*ast.SelectorExpr); ok && part.Sel.Name == "Namespace" && len(chain.Args) == 1 && !chain.Ellipsis.IsValid() {
				namespace = goflow.Code(chain.Args[0])
				chain, ok = part.X.(*ast.CallExpr)
				if !ok {
					return true
				}
			}
			part, ok := chain.Fun.(*ast.SelectorExpr)
			if !ok || part.Sel.Name != "Resource" || goflow.Code(part.X) != clientExpression {
				return true
			}
			roots[part.X] = true
			if len(chain.Args) != 1 || chain.Ellipsis.IsValid() || call.Ellipsis.IsValid() {
				failure = goflow.NewDiagnostic(fs, chain, "Resource", "unsupported dynamic resource call")
				return false
			}
			binding, err := catalog.Resolve(goflow.Code(chain.Args[0]))
			if err != nil {
				failure = goflow.NewDiagnostic(fs, chain.Args[0], "Resource", err.Error())
				return false
			}
			if (binding.Scope == "Namespaced" && namespace == "") || (binding.Scope == "Cluster" && namespace != "") {
				failure = goflow.NewDiagnostic(fs, sel.Sel, sel.Sel.Name, "dynamic namespace call disagrees with the declared scope of "+binding.Expression)
				return false
			}
			op, ok := dynamicOperations[sel.Sel.Name]
			if !ok || len(call.Args) != op.args {
				failure = goflow.NewDiagnostic(fs, sel.Sel, sel.Sel.Name, "unsupported dynamic operation or arguments")
				return false
			}
			operation := op.name
			if sel.Sel.Name == "Patch" {
				// The patch type decides the operation, so it must be a
				// literal: types.ApplyPatchType is apply, any other is patch.
				patchType := goflow.Code(call.Args[2])
				if !strings.HasPrefix(patchType, "types.") || !strings.HasSuffix(patchType, "PatchType") {
					failure = goflow.NewDiagnostic(fs, call.Args[2], "Patch", "dynamic patch type must be a types.*PatchType constant")
					return false
				}
				if patchType == "types.ApplyPatchType" {
					operation = "apply"
				}
			}
			out.Accesses = append(out.Accesses, Access{Method: name, Effect: methods[name], Operation: operation, Binding: binding, Namespace: namespace, Line: fs.Position(sel.Sel.Pos()).Line, MethodSHA256: goflow.Hash([]byte(goflow.Code(fn)))})
			return true
		})
		if failure != nil {
			return out, failure
		}
		count := 0
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			e, ok := n.(ast.Expr)
			if ok && goflow.Code(e) == clientExpression {
				count++
				if !roots[e] {
					failure = goflow.NewDiagnostic(fs, e, "client", "unsupported or escaped dynamic client in "+name)
				}
			}
			return true
		})
		if failure != nil {
			return out, failure
		}
		if count == 0 {
			return out, goflow.NewDiagnostic(fs, fn.Name, "method", "no dynamic access in selected method "+name)
		}
	}
	return out, nil
}

// CheckEffects joins extracted GVR/GVK operations to existing semantic summaries.
// A changed call cannot silently retain a summary for another resource or verb.
func (r Result) CheckEffects(entries []effect.Descriptor) error {
	registry, err := effect.NewRegistry(entries)
	if err != nil {
		return err
	}
	for _, a := range r.Accesses {
		d, err := registry.Lookup(a.Binding.Resource, a.Operation, "direct")
		if err != nil {
			return err
		}
		if d.ID != a.Effect {
			return fmt.Errorf("dynamic method %s maps to %s, expected %s", a.Method, d.ID, a.Effect)
		}
	}
	return nil
}
func (r Result) LikeC4() string {
	var b strings.Builder
	fmt.Fprintln(&b, "specification { element dynamicClientPart }\nmodel {\n dynamic_client = dynamicClientPart \"Dynamic Kubernetes client\"")
	ids := map[effect.Resource]string{}
	names := []string{"dynamic_client"}
	for _, a := range r.Accesses {
		id, ok := ids[a.Binding.Resource]
		if !ok {
			id = fmt.Sprintf("dynamic_resource%d", len(ids))
			ids[a.Binding.Resource] = id
			names = append(names, id)
			fmt.Fprintf(&b, " %s = dynamicClientPart %q { description %q }\n", id, a.Binding.Resource.Kind, a.Binding.GVR().String()+"; "+a.Binding.Scope)
		}
		fmt.Fprintf(&b, " dynamic_client -> %s %q\n", id, a.Method+": "+a.Operation+" via "+a.Binding.Expression)
	}
	fmt.Fprintf(&b, "}\nviews { view dynamic_client_access { title \"Dynamic resource access extracted from Go\"\n include %s\n } }\n", strings.Join(names, ", "))
	return b.String()
}

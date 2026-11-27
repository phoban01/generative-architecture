package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strconv"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/abstraction"
	"github.com/phoban01/generative-architecture/pkg/expr"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/project"
)

// A List call fills a list local (ADR-0011). The Go local is a list type
// such as *corev1.ConfigMapList; its Items field is the model's list of
// the identities of the listed objects, the symbol <local>_items. The
// summary binds $items to that symbol and $catalog to the identities of
// the abstracted objects that exist and match the call's options, in the
// order the abstraction declares them.

// listSymbol is the model symbol of the Items of a list local.
func listSymbol(local string) string { return local + "_items" }

// listItem returns the element type of a Kubernetes list type: a struct
// (or pointer to one) with an Items field of slice type. It reports false
// for any other type.
func listItem(t types.Type) (types.Type, bool) {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	st, ok := types.Unalias(t).Underlying().(*types.Struct)
	if !ok {
		return nil, false
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Name() != "Items" {
			continue
		}
		s, ok := types.Unalias(f.Type()).Underlying().(*types.Slice)
		if !ok {
			return nil, false
		}
		return s.Elem(), true
	}
	return nil, false
}

// keyObject is the abstracted object the controller's key addresses.
func (x *extraction) keyObject() (abstraction.Resource, abstraction.Object, bool) {
	for _, r := range x.resource {
		for _, o := range r.Objects {
			if o.ID == x.decl.Key {
				return r, o, true
			}
		}
	}
	return abstraction.Resource{}, abstraction.Object{}, false
}

// requestField reports the field of the reconcile request that v selects,
// Name or Namespace, through any embedding.
func (x *extraction) requestField(v ast.Expr) (string, bool) {
	sel, ok := ast.Unparen(v).(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	s, ok := x.info.Selections[sel]
	if !ok || s.Kind() != types.FieldVal {
		return "", false
	}
	root := sel.X
	for {
		inner, ok := ast.Unparen(root).(*ast.SelectorExpr)
		if !ok {
			break
		}
		root = inner.X
	}
	id, ok := ast.Unparen(root).(*ast.Ident)
	if !ok {
		return "", false
	}
	obj, ok := x.info.Uses[id].(*types.Var)
	if !ok {
		return "", false
	}
	if n, ok := types.Unalias(obj.Type()).(*types.Named); !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path()+"."+n.Obj().Name() != requestType {
		return "", false
	}
	name := s.Obj().Name()
	if name != "Name" && name != "Namespace" {
		return "", false
	}
	return name, true
}

// requestType is the type of the reconcile request.
const requestType = "sigs.k8s.io/controller-runtime/pkg/reconcile.Request"

// listCall binds a List call of the list local to the summary: $items to
// the local's list symbol and $catalog to the existing objects of r that
// the options keep. InNamespace keeps the objects of a namespace the
// abstraction fixes: a constant, the request's namespace (the key
// object's) or a field of an object local. MatchingFields with the
// request's name keeps every object of a resource whose owner reference
// targets the key's kind: the model's objects of that kind are the
// controller's own. Any other option is an error.
func (x *extraction) listCall(c goflow.Callee, call *ast.CallExpr, local string, r abstraction.Resource, e clientEffect) (goflow.Node, error) {
	namespace := ""
	for _, arg := range call.Args[2:] {
		t := x.info.TypeOf(arg)
		name := ""
		if t != nil {
			name = types.TypeString(t, nil)
		}
		switch name {
		case clientPackage + ".InNamespace":
			conv, ok := ast.Unparen(arg).(*ast.CallExpr)
			if !ok || len(conv.Args) != 1 {
				return goflow.Node{}, fmt.Errorf("%s: InNamespace must be a conversion of one expression", c)
			}
			v, err := x.keyValue(conv.Args[0])
			if err != nil {
				return goflow.Node{}, fmt.Errorf("%s: InNamespace: %v", c, err)
			}
			namespace = v
		case clientPackage + ".MatchingFields":
			lit, ok := ast.Unparen(arg).(*ast.CompositeLit)
			if !ok || len(lit.Elts) != 1 {
				return goflow.Node{}, fmt.Errorf("%s: MatchingFields must be a literal with one field", c)
			}
			kv, ok := lit.Elts[0].(*ast.KeyValueExpr)
			if !ok {
				return goflow.Node{}, fmt.Errorf("%s: MatchingFields must be a literal with one field", c)
			}
			if field, ok := x.requestField(kv.Value); !ok || field != "Name" {
				return goflow.Node{}, fmt.Errorf("%s: MatchingFields selects on %s; only an index on the request's name, an owner index, is supported", c, goflow.Code(kv.Value))
			}
			keyResource, _, ok := x.keyObject()
			if !ok {
				return goflow.Node{}, fmt.Errorf("%s: the controller's key is not an abstracted object", c)
			}
			owned := false
			for _, ref := range r.References {
				if ref.Owner && ref.TargetGVK == keyResource.GVK {
					owned = true
				}
			}
			if !owned {
				return goflow.Node{}, fmt.Errorf("%s: MatchingFields indexes %s by the request's name, but the abstraction declares no owner reference from %s to %s", c, r.GVK.Kind, r.GVK.Kind, keyResource.GVK.Kind)
			}
		default:
			return goflow.Node{}, fmt.Errorf("%s: unsupported List option %s; only InNamespace and MatchingFields are supported", c, goflow.Code(arg))
		}
	}
	parts := []string{}
	for _, o := range r.Objects {
		if namespace != "" && o.Namespace != namespace {
			continue
		}
		parts = append(parts, "("+abstraction.ExistsVariable(r.GVK.Kind, o.ID)+" ? ["+strconv.Quote(o.ID)+"] : [])")
	}
	catalog := "[]"
	if len(parts) > 0 {
		catalog = strings.Join(parts, " + ")
	}
	b := map[string]string{"$items": listSymbol(local), "$catalog": catalog, "$resource": r.GVK.Kind}
	var options map[string]string
	if namespace != "" {
		options = map[string]string{"namespace": namespace}
	}
	return goflow.Node{Kind: ir.OpList, Effect: e.Effect, Consistency: e.Consistency, Bindings: b, Options: options}, nil
}

// declaredChoices builds the frontend's expression bindings and pure
// callees from a controller declaration (ADR-0012). A choice binds the Go
// expression's text to an abstraction local the environment chooses; the
// local must be declared with choosable values.
func declaredChoices(abs abstraction.Abstraction, decl project.Controller) (map[string]expr.Expression, map[goflow.Callee]bool, error) {
	var bindings map[string]expr.Expression
	for _, expression := range sortedKeys(decl.Choices) {
		local := decl.Choices[expression]
		if _, ok := abs.ChoiceValues(local); !ok {
			return nil, nil, fmt.Errorf("controller %s: choice %q maps to %s, which is not an abstraction local with choosable values (a bool, or a local with values)", decl.Name, expression, local)
		}
		if bindings == nil {
			bindings = map[string]expr.Expression{}
		}
		bindings[expression] = expr.Expression(local)
	}
	var pure map[goflow.Callee]bool
	for _, symbol := range decl.Pure {
		pkg, receiver, method, err := project.PureSymbol(symbol)
		if err != nil {
			return nil, nil, fmt.Errorf("controller %s: %w", decl.Name, err)
		}
		if pure == nil {
			pure = map[goflow.Callee]bool{}
		}
		pure[goflow.Callee{Package: pkg, Receiver: receiver, Method: method}] = true
	}
	return bindings, pure, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// declareElement records the value variable of a range loop whose Go type
// is an abstracted resource: a loop element, an identity of that resource
// (ADR-0011). Its scope symbol is a string over the resource's object ids.
// It reports false when the type is a resource the abstraction does not
// select, after recording the diagnostic in x.declareErr.
func (x *extraction) declareElement(id *ast.Ident, scope map[string]expr.Symbol) bool {
	obj, ok := x.info.Defs[id].(*types.Var)
	if !ok {
		return true
	}
	gvk, ok, err := x.resourceOf(obj.Type())
	if err != nil || !ok {
		return true
	}
	r, ok := x.resource[gvk]
	if !ok {
		return true
	}
	x.elements[id.Name] = r
	ids := make([]string, 0, len(r.Objects))
	for _, o := range r.Objects {
		ids = append(ids, o.ID)
	}
	scope[id.Name] = expr.Symbol{Type: abstraction.TypeString, Quint: id.Name, Domain: ids}
	return true
}

// elementField reads a selected field through a loop element: a choice
// over the resource's objects, the element's identity selecting the
// object's variable, with the field's zero value when none matches.
func (x *extraction) elementField(root string, r abstraction.Resource, fieldPath []string) (expr.Expression, bool) {
	for _, f := range r.Fields {
		if abstraction.PathKey(fieldPath) != abstraction.PathKey(abstraction.JSONPath(f.Path)) {
			continue
		}
		v := abstraction.Initial(f.Type)
		for i := len(r.Objects) - 1; i >= 0; i-- {
			o := r.Objects[i]
			v = "(" + root + " == " + strconv.Quote(o.ID) + " ? " + abstraction.FieldVariable(r.GVK.Kind, o.ID, f) + " : " + v + ")"
		}
		return expr.Expression(v), true
	}
	return "", false
}

// elementCall summarizes a client call on the loop element root for the
// object id. Only a delete addresses an object without reading or writing
// its fields through locals, so only a delete is supported.
func (x *extraction) elementCall(c goflow.Callee, call *ast.CallExpr, root, id string, _ goflow.Convert) (goflow.Node, bool, error) {
	declared, ok := x.effects[c]
	if !ok {
		return goflow.Node{}, false, nil
	}
	r, ok := x.elements[root]
	if !ok {
		return goflow.Node{}, false, fmt.Errorf("%s: %s is not a loop element", c, root)
	}
	e, ok := effectFor(declared, r.GVK.Kind)
	if !ok {
		return goflow.Node{}, false, fmt.Errorf("%s is called with a %s, but no effect is declared for that kind", c, r.GVK.Kind)
	}
	if e.Operation != ir.OpDelete {
		return goflow.Node{}, false, fmt.Errorf("%s: only a delete is supported on the loop element %s; a %s needs the object's fields as locals", c, root, e.Operation)
	}
	n, err := x.bindings(c, e, r, id, root)
	return n, err == nil, err
}

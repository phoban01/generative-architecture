package watchgen

import (
	"github.com/phoban01/generative-architecture/pkg/effect"
	"github.com/phoban01/generative-architecture/pkg/goflow"
	"go/ast"
	"go/token"
	"strconv"
)

// Summary is a setup preamble statement the frontend recognised but does
// not model: a receiver default or a field index registration. The support
// report lists it as summarized.
type Summary struct {
	Kind      string `yaml:"kind"`
	Construct string `yaml:"construct"`
	Reason    string `yaml:"reason"`
	File      string `yaml:"file"`
	Line      int    `yaml:"line"`
}

// A setup preamble may construct unstructured objects and set each exact GVK.
// Two other statements are summarized, because they have no watch effect: a
// receiver default (if r.F == nil { r.F = <literal> }) and a field index
// registration (mgr.GetFieldIndexer().IndexField(ctx, &T{}, key, fn) with
// its error returned). Other statements remain unsupported: arbitrary setup
// control flow is not erased. Each unsupported statement gets its own
// diagnostic, and the check goes on with the next statement, so the report
// lists every preamble gap at once.
func unstructuredObjects(fs *token.FileSet, stmts []ast.Stmt, qualified func(ast.Expr) string, bindings map[string]effect.Resource, receiver, manager string) (map[string]effect.Resource, []Summary, error) {
	out := map[string]effect.Resource{}
	var ds []*goflow.Diagnostic
	var summaries []Summary
	for i := 0; i < len(stmts); {
		if !isObjectDeclaration(stmts[i]) {
			if s, ok := setupSummary(fs, stmts[i], qualified, receiver, manager); ok {
				summaries = append(summaries, s)
			} else {
				ds = append(ds, goflow.NewDiagnostic(fs, stmts[i], "preamble", "unsupported setup preamble statement; only unstructured object declarations are supported"))
			}
			i++
			continue
		}
		if i+1 == len(stmts) {
			ds = append(ds, goflow.NewDiagnostic(fs, stmts[i], "preamble", "unsupported setup preamble"))
			break
		}
		name, r, err := unstructuredObject(fs, stmts[i], stmts[i+1], qualified, bindings)
		i += 2
		if err != nil {
			d, ok := goflow.AsDiagnostic(err)
			if !ok {
				return nil, nil, err
			}
			ds = append(ds, d)
			continue
		}
		if _, ok := out[name.Name]; ok {
			ds = append(ds, goflow.NewDiagnostic(fs, name, "watch-object", "duplicate watch object "+name.Name))
			continue
		}
		out[name.Name] = r
	}
	if err := goflow.JoinDiagnostics(ds); err != nil {
		return nil, nil, err
	}
	return out, summaries, nil
}

// setupSummary recognises the two summarized preamble statements.
func setupSummary(fs *token.FileSet, s ast.Stmt, qualified func(ast.Expr) string, receiver, manager string) (Summary, bool) {
	ifs, ok := s.(*ast.IfStmt)
	if !ok || ifs.Else != nil {
		return Summary{}, false
	}
	at := func(kind, construct, reason string) Summary {
		pos := fs.Position(s.Pos())
		return Summary{Kind: kind, Construct: construct, Reason: reason, File: pos.Filename, Line: pos.Line}
	}
	// if r.F == nil { r.F = <literal> }
	if cond, ok := ifs.Cond.(*ast.BinaryExpr); ok && ifs.Init == nil && cond.Op == token.EQL && isNil(cond.Y) && len(ifs.Body.List) == 1 {
		field, ok := receiverField(cond.X, receiver)
		assign, isAssign := ifs.Body.List[0].(*ast.AssignStmt)
		if ok && isAssign && assign.Tok == token.ASSIGN && len(assign.Lhs) == 1 && len(assign.Rhs) == 1 {
			if target, ok := receiverField(assign.Lhs[0], receiver); ok && target == field && isLiteral(assign.Rhs[0]) {
				return at("setup", receiver+"."+field+" default", "receiver default; no watch effect"), true
			}
		}
	}
	// if err := mgr.GetFieldIndexer().IndexField(ctx, &T{}, key, fn); err != nil { return err }
	init, ok := ifs.Init.(*ast.AssignStmt)
	if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 1 || len(init.Rhs) != 1 {
		return Summary{}, false
	}
	call, ok := init.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 4 {
		return Summary{}, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "IndexField" {
		return Summary{}, false
	}
	indexer, ok := sel.X.(*ast.CallExpr)
	if !ok || len(indexer.Args) != 0 {
		return Summary{}, false
	}
	isel, ok := indexer.Fun.(*ast.SelectorExpr)
	if !ok || isel.Sel.Name != "GetFieldIndexer" {
		return Summary{}, false
	}
	if id, ok := isel.X.(*ast.Ident); !ok || id.Name != manager {
		return Summary{}, false
	}
	ptr, ok := call.Args[1].(*ast.UnaryExpr)
	if !ok || ptr.Op != token.AND {
		return Summary{}, false
	}
	lit, ok := ptr.X.(*ast.CompositeLit)
	if !ok || len(lit.Elts) != 0 {
		return Summary{}, false
	}
	key, ok := call.Args[2].(*ast.Ident)
	keyLit, isLit := call.Args[2].(*ast.BasicLit)
	keyText := ""
	switch {
	case ok:
		keyText = key.Name
	case isLit && keyLit.Kind == token.STRING:
		keyText = keyLit.Value
	default:
		return Summary{}, false
	}
	if !returnsErr(ifs, init.Lhs[0]) {
		return Summary{}, false
	}
	return at("index", "index "+qualified(lit.Type)+" by "+keyText, "field index registration; the extractor function is not modelled"), true
}

func isNil(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// receiverField returns the field name of r.F for the named receiver.
func receiverField(e ast.Expr, receiver string) (string, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != receiver {
		return "", false
	}
	return sel.Sel.Name, true
}

// isLiteral reports whether e is a basic or composite literal, a pointer
// to one, or a plain identifier such as a constant or true.
func isLiteral(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.BasicLit, *ast.CompositeLit, *ast.Ident:
		return true
	case *ast.UnaryExpr:
		return x.Op == token.AND && isLiteral(x.X)
	}
	return false
}

// returnsErr reports whether the body of ifs is `return <errVar>` guarded
// by `<errVar> != nil`.
func returnsErr(ifs *ast.IfStmt, errVar ast.Expr) bool {
	name, ok := errVar.(*ast.Ident)
	if !ok {
		return false
	}
	cond, ok := ifs.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ || !isNil(cond.Y) {
		return false
	}
	if id, ok := cond.X.(*ast.Ident); !ok || id.Name != name.Name {
		return false
	}
	if len(ifs.Body.List) != 1 {
		return false
	}
	ret, ok := ifs.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	id, ok := ret.Results[0].(*ast.Ident)
	return ok && id.Name == name.Name
}

// isObjectDeclaration reports whether s has the shape x := v, the first
// statement of an unstructured object declaration.
func isObjectDeclaration(s ast.Stmt) bool {
	assign, ok := s.(*ast.AssignStmt)
	return ok && assign.Tok == token.DEFINE && len(assign.Lhs) == 1 && len(assign.Rhs) == 1
}

// unstructuredObject reads one declaration x := &unstructured.Unstructured{}
// and the x.SetGroupVersionKind call that follows it.
func unstructuredObject(fs *token.FileSet, decl, set ast.Stmt, qualified func(ast.Expr) string, bindings map[string]effect.Resource) (*ast.Ident, effect.Resource, error) {
	var r effect.Resource
	assign := decl.(*ast.AssignStmt)
	name, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || name.Name == "_" {
		return nil, r, diag(fs, assign.Lhs[0], "watch-object", "invalid watch object name")
	}
	ptr, ok := assign.Rhs[0].(*ast.UnaryExpr)
	if !ok || ptr.Op != token.AND {
		return nil, r, diag(fs, assign.Rhs[0], "watch-object", "expected a pointer to an unstructured object")
	}
	lit, ok := ptr.X.(*ast.CompositeLit)
	if !ok || qualified(lit.Type) != "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured.Unstructured" || len(lit.Elts) != 0 {
		return nil, r, diag(fs, ptr.X, "watch-object", "unsupported watch object initializer")
	}
	stmt, ok := set.(*ast.ExprStmt)
	if !ok {
		return nil, r, diag(fs, set, "SetGroupVersionKind", "expected a SetGroupVersionKind call")
	}
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return nil, r, diag(fs, stmt.X, "SetGroupVersionKind", "expected exactly one GVK argument")
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || goflow.Code(sel.X) != name.Name || sel.Sel.Name != "SetGroupVersionKind" {
		return nil, r, diag(fs, call.Fun, "SetGroupVersionKind", "expected "+name.Name+".SetGroupVersionKind")
	}
	r, ok = bindings[goflow.Code(call.Args[0])]
	if !ok {
		value, ok := call.Args[0].(*ast.CompositeLit)
		if !ok || qualified(value.Type) != "k8s.io/apimachinery/pkg/runtime/schema.GroupVersionKind" {
			return nil, r, diag(fs, call.Args[0], "gvk", "unresolved dynamic watch GVK: supply a bounded GVK binding")
		}
		seen := map[string]bool{}
		for _, element := range value.Elts {
			kv, ok := element.(*ast.KeyValueExpr)
			if !ok {
				return nil, r, diag(fs, element, "gvk", "GVK requires named literal fields")
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || seen[key.Name] {
				return nil, r, diag(fs, kv.Key, "gvk", "invalid or duplicate GVK field")
			}
			seen[key.Name] = true
			str, ok := kv.Value.(*ast.BasicLit)
			if !ok || str.Kind != token.STRING {
				return nil, r, diag(fs, kv.Value, "gvk", "GVK requires constant strings or a bounded binding")
			}
			v, err := strconv.Unquote(str.Value)
			if err != nil {
				return nil, r, err
			}
			switch key.Name {
			case "Group":
				r.Group = v
			case "Version":
				r.Version = v
			case "Kind":
				r.Kind = v
			default:
				return nil, r, diag(fs, key, "gvk", "unknown GVK field "+key.Name)
			}
		}
	}
	if r.Version == "" || r.Kind == "" {
		return nil, r, diag(fs, call, "gvk", "unstructured watch requires a complete GVK")
	}
	return name, r, nil
}

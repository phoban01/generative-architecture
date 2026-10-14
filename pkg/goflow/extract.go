// Package goflow extracts bounded Go control flow using explicit expression and
// operation summaries. Unknown syntax fails closed. It is not a Go verifier.
package goflow

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"generative-architecture/pkg/expr"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

type Node struct {
	Callee      string                     `yaml:"callee,omitempty"`
	ID          int                        `yaml:"id"`
	Kind        string                     `yaml:"kind"`
	Source      string                     `yaml:"go"`
	Line        int                        `yaml:"line"`
	Expression  expr.Expression            `yaml:"expression,omitempty"`
	Next        int                        `yaml:"next"`
	Else        int                        `yaml:"else,omitempty"`
	ReturnError expr.Expression            `yaml:"returnError,omitempty"`
	Updates     map[string]expr.Expression `yaml:"updates,omitempty"`
	Results     []expr.Expression          `yaml:"results,omitempty"`
}
type Program struct {
	Entry        int    `yaml:"entry"`
	Nodes        []Node `yaml:"nodes"`
	SourceSHA256 string `yaml:"sourceSha256"`
	MethodSHA256 string `yaml:"methodSha256"`
}
type Return struct {
	Type  string
	Error bool
}

// Convert lowers a Go expression that must have the given model type. It is an
// alias so callbacks written against the plain function type keep compiling.
type Convert = func(ast.Expr, string) (expr.Expression, error)

// Summary callbacks must verify any helpers they abstract. The same frontend
// handles all control flow; callbacks describe effects and representation only.
// Call is consulted first, in typed mode only, for expression statements and
// assignments whose right-hand side is a call whose callee resolves to a
// symbol; Statement then sees any statement Call declined.
type Options struct {
	File, Receiver, Method string
	Compiler               expr.Compiler
	Scope                  map[string]expr.Symbol
	Bindings               map[string]expr.Expression
	Errors                 map[string]bool
	Targets                map[string]string
	Returns                []Return
	Call                   func(Callee, *ast.CallExpr, Convert) (Node, bool, error)
	Statement              func(ast.Stmt, Convert) (Node, bool, error)
}

func Code(n ast.Node) string {
	var b bytes.Buffer
	_ = format.Node(&b, token.NewFileSet(), n)
	return b.String()
}
func Hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func FindMethod(f *ast.File, receiver, method string) *ast.FuncDecl {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != method {
			continue
		}
		if receiver == "" && fn.Recv == nil {
			return fn
		}
		if fn.Recv != nil && len(fn.Recv.List) == 1 && (Code(fn.Recv.List[0].Type) == "*"+receiver || Code(fn.Recv.List[0].Type) == receiver) {
			return fn
		}
	}
	return nil
}

type extractor struct {
	opts    Options
	program Program
	fset    *token.FileSet
	info    *types.Info // nil in text mode; set by ExtractTyped
}

func (e *extractor) expression(n ast.Expr, want string) (expr.Expression, error) {
	v, err := e.translate(n)
	if err != nil {
		return "", err
	}
	if _, err = e.opts.Compiler.Compile(v, e.opts.Scope, want); err != nil {
		return "", err
	}
	return v, nil
}
func (e *extractor) translate(n ast.Expr) (expr.Expression, error) {
	if v, ok := e.opts.Bindings[Code(n)]; ok {
		return v, nil
	}
	switch x := n.(type) {
	case *ast.ParenExpr:
		v, err := e.translate(x.X)
		return "(" + v + ")", err
	case *ast.Ident:
		if e.info != nil {
			if v, ok, err := e.typedIdent(x); ok || err != nil {
				return v, err
			}
		}
		if x.Name == "true" || x.Name == "false" {
			return expr.Expression(x.Name), nil
		}
		if _, ok := e.opts.Scope[x.Name]; ok {
			return expr.Expression(x.Name), nil
		}
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			v, err := strconv.Unquote(x.Value)
			return expr.Expression(strconv.Quote(v)), err
		}
		if x.Kind == token.INT {
			v, err := strconv.ParseInt(x.Value, 0, 64)
			return expr.Expression(strconv.FormatInt(v, 10)), err
		}
	case *ast.SelectorExpr:
		v, err := e.translate(x.X)
		if err == nil {
			return v + "." + expr.Expression(x.Sel.Name), nil
		}
	case *ast.CallExpr:
		if e.info != nil {
			if v, ok, err := e.idiom(x); ok || err != nil {
				return v, err
			}
		}
	case *ast.UnaryExpr:
		if x.Op == token.NOT || x.Op == token.SUB {
			v, err := e.translate(x.X)
			return expr.Expression(x.Op.String()) + "(" + v + ")", err
		}
	case *ast.BinaryExpr:
		if e.opts.Errors[Code(x.X)] && Code(x.Y) == "nil" {
			v, err := e.translate(x.X)
			if err != nil {
				return "", err
			}
			if x.Op == token.EQL {
				return "!(" + v + ")", nil
			}
			if x.Op == token.NEQ {
				return v, nil
			}
		}
		switch x.Op {
		case token.EQL, token.NEQ, token.LAND, token.LOR, token.ADD, token.SUB, token.LSS, token.LEQ, token.GTR, token.GEQ:
			a, err := e.translate(x.X)
			if err != nil {
				return "", err
			}
			b, err := e.translate(x.Y)
			if err != nil {
				return "", err
			}
			return "(" + a + " " + expr.Expression(x.Op.String()) + " " + b + ")", nil
		}
	}
	return "", e.diagnose(n, "expression", "unsupported Go expression")
}
func (e *extractor) add(n Node, source ast.Node, next, other int) int {
	n.ID = len(e.program.Nodes)
	n.Source = Code(source)
	n.Line = e.fset.Position(source.Pos()).Line
	n.Next = next
	n.Else = other
	e.program.Nodes = append(e.program.Nodes, n)
	return n.ID
}
func (e *extractor) block(stmts []ast.Stmt, next int) (int, error) {
	for i := len(stmts) - 1; i >= 0; i-- {
		n, err := e.statement(stmts[i], next)
		if err != nil {
			return 0, err
		}
		next = n
	}
	return next, nil
}
func (e *extractor) statement(s ast.Stmt, next int) (int, error) {
	if e.opts.Call != nil {
		if call := statementCall(s); call != nil {
			if c, ok := e.callee(call); ok {
				n, ok, err := e.opts.Call(c, call, e.expression)
				if err != nil {
					return 0, err
				}
				if ok {
					return e.summarized(n, s, next)
				}
			}
		}
	}
	if e.opts.Statement != nil {
		n, ok, err := e.opts.Statement(s, e.expression)
		if err != nil {
			return 0, err
		}
		if ok {
			return e.summarized(n, s, next)
		}
	}
	switch x := s.(type) {
	case *ast.IfStmt:
		yes, err := e.block(x.Body.List, next)
		if err != nil {
			return 0, err
		}
		no := next
		if x.Else != nil {
			switch y := x.Else.(type) {
			case *ast.BlockStmt:
				no, err = e.block(y.List, next)
			case *ast.IfStmt:
				no, err = e.statement(y, next)
			default:
				err = e.diagnose(x.Else, "else", "unsupported else clause")
			}
			if err != nil {
				return 0, err
			}
		}
		condition, err := e.expression(x.Cond, "bool")
		if err != nil {
			return 0, err
		}
		branch := e.add(Node{Kind: "branch", Expression: condition}, x.Cond, yes, no)
		if x.Init != nil {
			return e.statement(x.Init, branch)
		}
		return branch, nil
	case *ast.ReturnStmt:
		if len(x.Results) != len(e.opts.Returns) {
			return 0, e.diagnose(s, "return", fmt.Sprintf("unsupported return arity %d; expected %d", len(x.Results), len(e.opts.Returns)))
		}
		values := []expr.Expression{}
		for i, result := range x.Results {
			r := e.opts.Returns[i]
			var v expr.Expression
			var err error
			if r.Error && Code(result) == "nil" {
				v = "false"
			} else {
				if r.Error && !e.opts.Errors[Code(result)] {
					return 0, e.diagnose(result, "error", "unsummarized returned error")
				}
				v, err = e.expression(result, r.Type)
			}
			if err != nil {
				return 0, err
			}
			values = append(values, v)
		}
		n := Node{Kind: "return", Results: values}
		if len(values) > 0 {
			n.Expression = values[0]
		}
		if len(values) > 1 {
			n.ReturnError = values[1]
		}
		return e.add(n, s, -1, 0), nil
	case *ast.AssignStmt:
		if len(x.Lhs) == 1 && len(x.Rhs) == 1 && (x.Tok == token.ASSIGN || x.Tok == token.DEFINE) {
			target, ok := e.opts.Targets[Code(x.Lhs[0])]
			if ok {
				symbol, ok := e.opts.Scope[target]
				if !ok {
					return 0, e.diagnose(x, "assign", "unknown assignment target "+target)
				}
				v, err := e.expression(x.Rhs[0], symbol.Type)
				if err != nil {
					return 0, err
				}
				return e.add(Node{Kind: "assign", Updates: map[string]expr.Expression{target: v}}, s, next, 0), nil
			}
		}
	}
	return 0, e.diagnose(s, statementKind(s), "unsupported Go statement")
}

// Extract lowers the selected method of a single parsed file. Identifiers are
// resolved by name against Options; see ExtractTyped for symbol resolution.
func Extract(source []byte, opts Options) (*Program, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, opts.File, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	method := FindMethod(f, opts.Receiver, opts.Method)
	if method == nil || method.Body == nil {
		return nil, fmt.Errorf("method not found: %s.%s", opts.Receiver, opts.Method)
	}
	return (&extractor{opts: opts, fset: fset}).run(source, method)
}

// run lowers method's body. Scope and Errors are copied so that symbols the
// extractor declares itself never leak back into the caller's Options.
func (e *extractor) run(source []byte, method *ast.FuncDecl) (*Program, error) {
	e.opts.Scope = copyScope(e.opts.Scope)
	e.opts.Errors = copyErrors(e.opts.Errors)
	if e.info != nil {
		e.declareErrors(method.Body)
	}
	e.program.SourceSHA256 = Hash(source)
	e.program.MethodSHA256 = Hash([]byte(Code(method)))
	var err error
	e.program.Entry, err = e.block(method.Body.List, -1)
	if err != nil {
		return nil, err
	}
	for _, n := range e.program.Nodes {
		if n.Kind != "return" && (n.Next < 0 || (n.Kind == "branch" && n.Else < 0)) {
			return nil, &Diagnostic{File: e.opts.File, Line: n.Line, Construct: "fallthrough", Reason: "unsupported fallthrough: control reaches the end of the method without a return", Source: n.Source}
		}
	}
	return &e.program, nil
}

// VerifyDeclarations pins explicitly summarized local helper bodies and values.
// Normalize before hashing so formatting-only edits do not invalidate semantics.
func VerifyDeclarations(source []byte, expected map[string]string) (map[string]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "summaries.go", source, 0)
	if err != nil {
		return nil, err
	}
	found := map[string]string{}
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			if x.Recv == nil {
				found[x.Name.Name] = Code(x)
			}
		case *ast.GenDecl:
			for _, sp := range x.Specs {
				v, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, n := range v.Names {
					if i < len(v.Values) {
						found[n.Name] = Code(v.Values[i])
					}
				}
			}
		}
	}
	out := map[string]string{}
	for name, want := range expected {
		actual, ok := found[name]
		if !ok {
			return nil, fmt.Errorf("missing summarized declaration %s", name)
		}
		if strings.HasPrefix(strings.TrimSpace(want), "func ") {
			p, err := parser.ParseFile(token.NewFileSet(), "expected.go", "package p\n"+want, 0)
			if err != nil {
				return nil, err
			}
			want = Code(p.Decls[0])
		} else {
			x, err := parser.ParseExpr(want)
			if err != nil {
				return nil, err
			}
			want = Code(x)
		}
		if want != actual {
			return nil, fmt.Errorf("helper or value %s changed; its explicit summary needs review", name)
		}
		out[name] = Hash([]byte(actual))
	}
	return out, nil
}

// summarized adds a node a summary callback produced for s. In typed mode an
// assignment binds each left-hand name to the node's Results so the summary
// can update the corresponding model symbols.
func (e *extractor) summarized(n Node, s ast.Stmt, next int) (int, error) {
	if x, ok := s.(*ast.AssignStmt); ok && e.info != nil {
		var err error
		if n, err = e.bindResults(n, x); err != nil {
			return 0, err
		}
	}
	return e.add(n, s, next, 0), nil
}

// statementCall returns the call a statement consists of: an expression
// statement that is a call, or an assignment from a single call.
func statementCall(s ast.Stmt) *ast.CallExpr {
	switch x := s.(type) {
	case *ast.ExprStmt:
		if call, ok := x.X.(*ast.CallExpr); ok {
			return call
		}
	case *ast.AssignStmt:
		if len(x.Rhs) == 1 && (x.Tok == token.ASSIGN || x.Tok == token.DEFINE) {
			if call, ok := x.Rhs[0].(*ast.CallExpr); ok {
				return call
			}
		}
	}
	return nil
}

func copyScope(s map[string]expr.Symbol) map[string]expr.Symbol {
	out := make(map[string]expr.Symbol, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}
func copyErrors(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// statementKind names a statement for diagnostics.
func statementKind(s ast.Stmt) string {
	switch s.(type) {
	case *ast.ForStmt:
		return "for"
	case *ast.RangeStmt:
		return "range"
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		return "switch"
	case *ast.SelectStmt:
		return "select"
	case *ast.GoStmt:
		return "go"
	case *ast.DeferStmt:
		return "defer"
	case *ast.AssignStmt:
		return "assign"
	case *ast.ExprStmt:
		return "call"
	case *ast.DeclStmt:
		return "declaration"
	case *ast.LabeledStmt, *ast.BranchStmt:
		return "label"
	case *ast.IncDecStmt:
		return "incdec"
	case *ast.SendStmt:
		return "send"
	}
	return "statement"
}

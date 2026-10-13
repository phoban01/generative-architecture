// Package behaviorgen is a deliberately small, fail-closed Go AST compiler for
// the demo publication process. It extracts branching and writes; external API
// operations use explicit summaries. It is not a general Go verifier.
package behaviorgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"generative-architecture/examples/machinevendor/architecture"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/goflow"
)

type Config struct {
	Component             string                  `yaml:"component"`
	Action                string                  `yaml:"action"`
	Source                string                  `yaml:"-"`
	Receiver              string                  `yaml:"-"`
	Method                string                  `yaml:"-"`
	AllowStalePublication bool                    `yaml:"allowStalePublication"`
	Requirements          []architecture.Property `yaml:"requirements"`
}
type Node struct {
	ID          int    `yaml:"id"`
	Kind        string `yaml:"kind"`
	Source      string `yaml:"go"`
	Line        int    `yaml:"line"`
	Expression  string `yaml:"expression,omitempty"`
	Next        int    `yaml:"next"`
	Else        int    `yaml:"else,omitempty"`
	ReturnError string `yaml:"returnError,omitempty"`
}
type Program struct {
	Entry        int               `yaml:"entry"`
	Nodes        []Node            `yaml:"nodes"`
	SourceSHA256 string            `yaml:"sourceSha256"`
	MethodSHA256 string            `yaml:"methodSha256"`
	Summaries    map[string]string `yaml:"summaries"`
	cfg          Config
}

func code(n ast.Node) string {
	var b bytes.Buffer
	_ = format.Node(&b, token.NewFileSet(), n)
	return b.String()
}
func scope() map[string]architecture.Symbol {
	out := map[string]architecture.Symbol{}
	for k, t := range map[string]string{"observed": "Assignment", "claim": "Assignment", "lease": "Assignment", "err": "bool", "publishedMachineId": "string", "localMachineId": "string", "event": "string"} {
		out[k] = architecture.Symbol{Type: t, Quint: k}
	}
	return out
}
func findMethod(file *ast.File, c Config) *ast.FuncDecl {
	for _, decl := range file.Decls {
		f, ok := decl.(*ast.FuncDecl)
		if !ok || f.Name.Name != c.Method || f.Recv == nil || len(f.Recv.List) != 1 {
			continue
		}
		if code(f.Recv.List[0].Type) == "*"+c.Receiver {
			return f
		}
	}
	return nil
}

func Extract(source []byte, c Config) (*Program, error) {
	if c.Receiver == "" || c.Method == "" {
		return nil, fmt.Errorf("a resolved process method is required")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, c.Source, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	method := findMethod(file, c)
	if method == nil || method.Body == nil {
		return nil, fmt.Errorf("method not found")
	}
	if code(method.Type) != `func(ctx context.Context, key client.ObjectKey, observed Assignment) (bool, error)` {
		return nil, fmt.Errorf("unsupported publication signature")
	}
	imports := map[string]string{}
	for _, i := range file.Imports {
		path, _ := strconv.Unquote(i.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if i.Name != nil {
			name = i.Name.Name
		}
		imports[name] = path
	}
	if imports["unstructured"] != "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured" || imports["client"] != "sigs.k8s.io/controller-runtime/pkg/client" {
		return nil, fmt.Errorf("boundary package identity changed")
	}
	// These are explicit dependency summaries, not source-derived behavior.
	expected := map[string]string{
		"get": `func get(ctx context.Context, c client.Reader, gvk schema.GroupVersionKind, key client.ObjectKey) (*unstructured.Unstructured, error) {
 o := object(gvk, key)
 return o, c.Get(ctx, key, o)
}`,
		"object": `func object(gvk schema.GroupVersionKind, key client.ObjectKey) *unstructured.Unstructured {
 o := &unstructured.Unstructured{Object: map[string]any{}}
 o.SetGroupVersionKind(gvk)
 o.SetNamespace(key.Namespace)
 o.SetName(key.Name)
 return o
}`,
		"assignmentOf": `func assignmentOf(o *unstructured.Unstructured) Assignment {
 epoch, _, _ := unstructured.NestedInt64(o.Object, "status", "epoch")
 return Assignment{MachineID: str(o, "status", "machineId"), Epoch: epoch}
}`,
		"str": `func str(o *unstructured.Unstructured, fields ...string) string {
 v, _, _ := unstructured.NestedString(o.Object, fields...)
 return v
}`,
	}
	summaries := map[string]string{}
	for name, body := range expected {
		parsed, err := parser.ParseFile(token.NewFileSet(), "summary.go", "package p\n"+body, 0)
		if err != nil {
			return nil, err
		}
		want := code(parsed.Decls[0])
		found := false
		for _, decl := range file.Decls {
			f, ok := decl.(*ast.FuncDecl)
			if !ok || f.Recv != nil || f.Name.Name != name {
				continue
			}
			found = true
			if code(f) != want {
				return nil, fmt.Errorf("helper %s changed; its explicit summary needs review", name)
			}
			summaries[name] = hash([]byte(code(f)))
		}
		if !found {
			return nil, fmt.Errorf("missing summarized helper %s", name)
		}
	}
	// The read summaries rely on these concrete resource identities.
	expectedGVKs := map[string]string{
		"claimGVK": `schema.GroupVersionKind{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "MachineClaim"}`,
		"infraGVK": `schema.GroupVersionKind{Group: "demo.architecture.local", Version: "v1alpha1", Kind: "InfraMachine"}`,
	}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			v := spec.(*ast.ValueSpec)
			for i, name := range v.Names {
				want, ok := expectedGVKs[name.Name]
				if !ok {
					continue
				}
				if i >= len(v.Values) || code(v.Values[i]) != want {
					return nil, fmt.Errorf("resource identity %s changed; review API summary", name.Name)
				}
				summaries[name.Name] = hash([]byte(want))
				delete(expectedGVKs, name.Name)
			}
		}
	}
	if len(expectedGVKs) > 0 {
		return nil, fmt.Errorf("missing summarized resource identities")
	}

	options := goflow.Options{
		File: c.Source, Receiver: c.Receiver, Method: c.Method,
		Compiler: expr.Compiler{Records: map[string]map[string]string{"Assignment": {"machineId": "string", "epoch": "int"}}},
		Scope:    scope(), Bindings: map[string]expr.Expression{"observed.MachineID": "observed.machineId", "observed.Epoch": "observed.epoch", "r.AllowStalePublication": expr.Expression(strconv.FormatBool(c.AllowStalePublication)), "assignmentOf(claim)": "claim"},
		Errors: map[string]bool{"err": true}, Returns: []goflow.Return{{Type: "bool"}, {Type: "bool", Error: true}},
	}
	options.Statement = func(s ast.Stmt, expression func(ast.Expr, string) (expr.Expression, error)) (goflow.Node, bool, error) {
		switch code(s) {
		case "claim, err := get(ctx, r.APIReader, claimGVK, key)":
			return goflow.Node{Kind: "readClaim"}, true, nil
		case "infra, err := get(ctx, r.Client, infraGVK, key)":
			return goflow.Node{Kind: "readInfra"}, true, nil
		case "err = r.Client.Status().Update(ctx, infra)":
			return goflow.Node{Kind: "updateStatus"}, true, nil
		}
		x, ok := s.(*ast.AssignStmt)
		if ok && len(x.Lhs) == 1 && code(x.Lhs[0]) == "err" && x.Tok == token.ASSIGN && len(x.Rhs) == 1 {
			call, ok := x.Rhs[0].(*ast.CallExpr)
			if ok && code(call.Fun) == "unstructured.SetNestedField" && len(call.Args) == 4 && code(call.Args[0]) == "infra.Object" && code(call.Args[2]) == `"status"` && code(call.Args[3]) == `"machineId"` {
				value, err := expression(call.Args[1], "string")
				return goflow.Node{Kind: "setField", Expression: value}, true, err
			}
		}
		return goflow.Node{}, false, nil
	}
	generic, err := goflow.Extract(source, options)
	if err != nil {
		return nil, err
	}
	p := &Program{Entry: generic.Entry, SourceSHA256: generic.SourceSHA256, MethodSHA256: generic.MethodSHA256, Summaries: summaries, cfg: c}
	// Compatibility projection for existing machine-vendor models and evidence.
	for _, n := range generic.Nodes {
		expression, returnError := "", ""
		if n.Expression != "" {
			v, err := architecture.CompileCEL(n.Expression, scope(), "")
			if err != nil {
				return nil, err
			}
			expression = v.Quint
		}
		if n.ReturnError != "" {
			v, err := architecture.CompileCEL(n.ReturnError, scope(), "bool")
			if err != nil {
				return nil, err
			}
			returnError = v.Quint
		}
		p.Nodes = append(p.Nodes, Node{ID: n.ID, Kind: n.Kind, Source: n.Source, Line: n.Line, Expression: expression, Next: n.Next, Else: n.Else, ReturnError: returnError})
	}
	return p, nil
}

// RemoveStaleCheck creates a separate source artifact; it never edits the live file.
func RemoveStaleCheck(source []byte, c Config) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, c.Source, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	method := findMethod(f, c)
	if method == nil {
		return nil, fmt.Errorf("method missing")
	}
	changed := 0
	ast.Inspect(method.Body, func(n ast.Node) bool {
		branch, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		expr, ok := branch.Cond.(*ast.BinaryExpr)
		if ok && expr.Op == token.LOR && code(expr.X) == `observed.MachineID == ""` && code(expr.Y) == `(!r.AllowStalePublication && observed != assignmentOf(claim))` {
			branch.Cond = expr.X
			changed++
		}
		return true
	})
	if changed != 1 {
		return nil, fmt.Errorf("expected exactly one stale-check mutation site, found %d", changed)
	}
	var out bytes.Buffer
	err = format.Node(&out, fset, f)
	return out.Bytes(), err
}

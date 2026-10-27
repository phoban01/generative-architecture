// Package expr type-checks a deliberately bounded CEL subset and lowers it to Quint.
package expr

import (
	"errors"
	"fmt"
	"go.yaml.in/yaml/v3"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

type Symbol struct {
	Type  string `yaml:"type"`
	Quint string `yaml:"quint"`
}

var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
var mapType = regexp.MustCompile(`^map\(string, ([A-Za-z][A-Za-z0-9_]*)\)$`)

// Compiler owns the record schema; there are no built-in domain types.
type Compiler struct{ Records map[string]map[string]string }

type Expression string

func (e *Expression) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return fmt.Errorf("line %d: CEL expressions must be strings", n.Line)
	}
	*e = Expression(n.Value)
	return nil
}

func (compiler Compiler) QuintType(t string) (string, error) {
	if t == "string" {
		return "str", nil
	}
	if t == "int" || t == "bool" || compiler.Records[t] != nil {
		return t, nil
	}
	if m := mapType.FindStringSubmatch(t); m != nil {
		v, err := compiler.QuintType(m[1])
		if err != nil {
			return "", err
		}
		return "str -> " + v, nil
	}
	return "", fmt.Errorf("unsupported CEL type %s", t)
}
func mapElement(t string) (string, error) {
	m := mapType.FindStringSubmatch(t)
	if m == nil {
		return "", fmt.Errorf("expected map state type: %s", t)
	}
	return m[1], nil
}
func copyScope(s map[string]Symbol) map[string]Symbol {
	o := map[string]Symbol{}
	for k, v := range s {
		o[k] = v
	}
	return o
}
func dotted(e *exprpb.Expr) string {
	if e == nil {
		return ""
	}
	if i := e.GetIdentExpr(); i != nil {
		return i.Name
	}
	if s := e.GetSelectExpr(); s != nil {
		p := dotted(s.Operand)
		if p != "" {
			return p + "." + s.Field
		}
	}
	return ""
}
func (compiler Compiler) Compile(source Expression, scope map[string]Symbol, expected string) (Symbol, error) {
	env, err := cel.NewEnv()
	if err != nil {
		return Symbol{}, err
	}
	ast, issues := env.Parse(string(source))
	if issues.Err() != nil {
		return Symbol{}, fmt.Errorf("invalid CEL %q: %w", source, issues.Err())
	}
	result, err := compiler.lowerCEL(ast.Expr(), scope)
	var at *locatedError
	if errors.As(err, &at) {
		l := ast.NativeRep().SourceInfo().GetStartLocation(at.id)
		err = fmt.Errorf("line %d, column %d: %w", l.Line(), l.Column()+1, at.err)
	}
	if err == nil && expected != "" && result.Type != expected {
		err = fmt.Errorf("CEL type mismatch: expected %s, got %s", expected, result.Type)
	}
	if err != nil {
		return Symbol{}, fmt.Errorf("%w\n  expression: %s", err, source)
	}
	return result, nil
}
func (compiler Compiler) lowerCEL(e *exprpb.Expr, scope map[string]Symbol) (Symbol, error) {
	bad := func(f string, a ...any) (Symbol, error) { return located(e, fmt.Errorf(f, a...)) }
	if s, ok := scope[dotted(e)]; ok {
		return s, nil
	}
	if c := e.GetConstExpr(); c != nil {
		switch v := c.ConstantKind.(type) {
		case *exprpb.Constant_StringValue:
			return Symbol{"string", strconv.Quote(v.StringValue)}, nil
		case *exprpb.Constant_BoolValue:
			return Symbol{"bool", strconv.FormatBool(v.BoolValue)}, nil
		case *exprpb.Constant_Int64Value:
			return Symbol{"int", strconv.FormatInt(v.Int64Value, 10)}, nil
		default:
			return bad("unsupported CEL literal")
		}
	}
	if i := e.GetIdentExpr(); i != nil {
		return bad("unknown CEL symbol %s; cross-component reads must use a bound port", i.Name)
	}
	if s := e.GetSelectExpr(); s != nil {
		if s.TestOnly {
			return bad("CEL has() is unsupported")
		}
		v, err := compiler.lowerCEL(s.Operand, scope)
		if err != nil {
			return v, err
		}
		t, ok := compiler.Records[v.Type][s.Field]
		if !ok {
			return bad("unknown CEL field %s.%s", v.Type, s.Field)
		}
		return Symbol{t, v.Quint + "." + s.Field}, nil
	}
	if s := e.GetStructExpr(); s != nil {
		if s.MessageName != "" {
			return bad("message literals unsupported; use declared record shapes")
		}
		values := map[string]Symbol{}
		for _, entry := range s.Entries {
			key := entry.GetMapKey().GetConstExpr()
			if key == nil || entry.OptionalEntry {
				return bad("record keys must be constant strings")
			}
			k, ok := key.ConstantKind.(*exprpb.Constant_StringValue)
			if !ok {
				return bad("record keys must be strings")
			}
			if _, exists := values[k.StringValue]; exists {
				return bad("duplicate record field")
			}
			v, err := compiler.lowerCEL(entry.Value, scope)
			if err != nil {
				return v, err
			}
			values[k.StringValue] = v
		}
		matches := []string{}
		for name, fields := range compiler.Records {
			if len(fields) != len(values) {
				continue
			}
			match := true
			for key, typ := range fields {
				if values[key].Type != typ {
					match = false
				}
			}
			if match {
				matches = append(matches, name)
			}
		}
		if len(matches) != 1 {
			return bad("record literal must match exactly one declared shape (matched %d)", len(matches))
		}
		// Stable schema ordering makes generation reproducible.
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		sort.Strings(names)
		fields := []string{}
		for _, name := range names {
			fields = append(fields, name+": "+values[name].Quint)
		}
		return Symbol{matches[0], "{ " + strings.Join(fields, ", ") + " }"}, nil
	}
	if c := e.GetComprehensionExpr(); c != nil {
		q, ok := quantifier(c)
		if !ok {
			return bad("only CEL all() and exists() comprehensions are supported")
		}
		if _, ok := scope[c.IterVar]; ok || !identifier.MatchString(c.IterVar) {
			return bad("CEL binder shadows existing symbol")
		}
		// The range must be a declared finite domain: Quint quantifies over
		// exactly the declared values, so the lowering is sound only there.
		name := dotted(c.IterRange)
		if _, declared := scope[name]; name != "" && !declared {
			return located(c.IterRange, fmt.Errorf("%s() ranges over %s, which is not a declared domain", q.name, name))
		}
		r, err := compiler.lowerCEL(c.IterRange, scope)
		if err != nil {
			return r, err
		}
		if r.Type != Domain {
			return located(c.IterRange, fmt.Errorf("%s() requires a declared string domain, got %s", q.name, r.Type))
		}
		nested := copyScope(scope)
		nested[c.IterVar] = Symbol{"string", c.IterVar}
		b, err := compiler.lowerCEL(q.predicate, nested)
		if err != nil {
			return b, err
		}
		if b.Type != "bool" {
			return bad("%s() predicate must be bool", q.name)
		}
		return Symbol{"bool", r.Quint + "." + q.quint + "(" + c.IterVar + " => " + b.Quint + ")"}, nil
	}
	if c := e.GetCallExpr(); c != nil {
		if c.Target != nil {
			return bad("unsupported CEL receiver function %s", c.Function)
		}
		a := []Symbol{}
		for _, v := range c.Args {
			s, err := compiler.lowerCEL(v, scope)
			if err != nil {
				return s, err
			}
			a = append(a, s)
		}
		if c.Function == "_[_]" && len(a) == 2 {
			t, err := mapElement(a[0].Type)
			if err != nil {
				return Symbol{}, err
			}
			if a[1].Type != "string" {
				return bad("CEL map key must be string")
			}
			return Symbol{t, a[0].Quint + ".get(" + a[1].Quint + ")"}, nil
		}
		if len(a) == 1 {
			if c.Function == "!_" && a[0].Type == "bool" {
				return Symbol{"bool", "not(" + a[0].Quint + ")"}, nil
			}
			if c.Function == "-_" && a[0].Type == "int" {
				return Symbol{"int", "(0 - " + a[0].Quint + ")"}, nil
			}
		}
		if c.Function == "_?_:_" && len(a) == 3 {
			if a[0].Type != "bool" || a[1].Type != a[2].Type {
				return bad("CEL conditional type mismatch")
			}
			return Symbol{a[1].Type, "(if (" + a[0].Quint + ") " + a[1].Quint + " else " + a[2].Quint + ")"}, nil
		}
		ops := map[string]string{"_&&_": "and", "_||_": "or", "_==_": "==", "_!=_": "!=", "_+_": "+", "_-_": "-", "_<_": "<", "_<=_": "<=", "_>_": ">", "_>=_": ">="}
		if op, ok := ops[c.Function]; ok && len(a) == 2 {
			if a[0].Type != a[1].Type {
				return bad("CEL type mismatch")
			}
			t := a[0].Type
			out := "bool"
			switch op {
			case "and", "or":
				if t != "bool" {
					return bad("CEL boolean type mismatch")
				}
			case "+", "-", "<", "<=", ">", ">=":
				if t != "int" {
					return bad("CEL integer type mismatch")
				}
				if op == "+" || op == "-" {
					out = "int"
				}
			default:
				if !slices.Contains([]string{"string", "int", "bool"}, t) && compiler.Records[t] == nil {
					return bad("unsupported equality type")
				}
			}
			return Symbol{out, "(" + a[0].Quint + " " + op + " " + a[1].Quint + ")"}, nil
		}
		return bad("unsupported CEL function %s", c.Function)
	}
	return bad("unsupported CEL syntax")
}

// Domain is the type of a declared finite string domain. all() and
// exists() range only over symbols of this type.
const Domain = "domain(string)"

// comprehension is a recognised CEL quantifier macro.
type comprehension struct {
	// name is the CEL macro, quint the Quint set operator.
	name, quint string
	predicate   *exprpb.Expr
}

// quantifier recognises the expansions of the CEL macros all() and
// exists(), as the cel-go parser writes them:
//
//	all:    init true,  condition @not_strictly_false(acc),  step acc && p
//	exists: init false, condition @not_strictly_false(!acc), step acc || p
//
// Both return the accumulator. Any other comprehension is rejected.
func quantifier(c *exprpb.Expr_Comprehension) (comprehension, bool) {
	init := c.AccuInit.GetConstExpr()
	step := c.LoopStep.GetCallExpr()
	condition := c.LoopCondition.GetCallExpr()
	if c.IterVar2 != "" || init == nil || step == nil || len(step.Args) != 2 || dotted(step.Args[0]) != c.AccuVar || condition == nil || condition.Function != "@not_strictly_false" || len(condition.Args) != 1 || dotted(c.Result) != c.AccuVar {
		return comprehension{}, false
	}
	if _, ok := init.ConstantKind.(*exprpb.Constant_BoolValue); !ok {
		return comprehension{}, false
	}
	accumulator := condition.Args[0]
	if init.GetBoolValue() && step.Function == "_&&_" && dotted(accumulator) == c.AccuVar {
		return comprehension{"all", "forall", step.Args[1]}, true
	}
	negated := accumulator.GetCallExpr()
	if !init.GetBoolValue() && step.Function == "_||_" && negated != nil && negated.Function == "!_" && len(negated.Args) == 1 && dotted(negated.Args[0]) == c.AccuVar {
		return comprehension{"exists", "exists", step.Args[1]}, true
	}
	return comprehension{}, false
}

// locatedError carries the CEL node an error arose at. Compile turns the
// node into a line and column of the source.
type locatedError struct {
	id  int64
	err error
}

func (e *locatedError) Error() string { return e.err.Error() }
func (e *locatedError) Unwrap() error { return e.err }

func located(e *exprpb.Expr, err error) (Symbol, error) {
	return Symbol{}, &locatedError{id: e.GetId(), err: err}
}

// Evaluator runs original CEL, independently of the lowering logic above.
type Evaluator struct {
	env      *cel.Env
	programs map[Expression]cel.Program
	Count    int
}

func NewEvaluator() (*Evaluator, error) {
	env, err := cel.NewEnv()
	return &Evaluator{env: env, programs: map[Expression]cel.Program{}}, err
}
func (e *Evaluator) Eval(source Expression, vars map[string]any) (any, error) {
	p, ok := e.programs[source]
	if !ok {
		ast, issues := e.env.Parse(string(source))
		if issues.Err() != nil {
			return nil, issues.Err()
		}
		var err error
		p, err = e.env.Program(ast)
		if err != nil {
			return nil, err
		}
		e.programs[source] = p
	}
	e.Count++
	v, _, err := p.Eval(vars)
	if err != nil {
		return nil, err
	}
	return native(v)
}
func native(v ref.Val) (any, error) {
	if types.IsError(v) || types.IsUnknown(v) {
		return nil, fmt.Errorf("CEL evaluation: %v", v)
	}
	switch x := v.(type) {
	case types.Bool:
		return bool(x), nil
	case types.String:
		return string(x), nil
	case types.Int:
		return int64(x), nil
	case traits.Mapper:
		m := map[string]any{}
		iter := x.Iterator()
		for iter.HasNext() == types.True {
			k := iter.Next()
			s, ok := k.(types.String)
			if !ok {
				return nil, fmt.Errorf("non-string CEL map key")
			}
			value, err := native(x.Get(k))
			if err != nil {
				return nil, err
			}
			m[string(s)] = value
		}
		return m, nil
	}
	return nil, fmt.Errorf("unsupported CEL result %v", v.Type())
}

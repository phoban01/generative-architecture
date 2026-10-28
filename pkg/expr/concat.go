package expr

import (
	"fmt"
	"strconv"

	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// maxConcatenations bounds the constants a string concatenation expands
// to, as each conditional operand doubles them.
const maxConcatenations = 64

// stringValue is a string expression whose value is known up to its
// conditions: a constant, or a choice between two string values. Quint
// has no string operations, so string concatenation is lowered by
// distributing it over the choices down to constants.
type stringValue struct {
	constant *string
	// condition is the Quint condition of a choice.
	condition string
	then, els *stringValue
	size      int
}

func constantString(s string) *stringValue { return &stringValue{constant: &s, size: 1} }

func (v *stringValue) quint() string {
	if v.constant != nil {
		return strconv.Quote(*v.constant)
	}
	return "(if (" + v.condition + ") " + v.then.quint() + " else " + v.els.quint() + ")"
}

// concat distributes a + b over the choices of both operands.
func concat(a, b *stringValue) *stringValue {
	switch {
	case a.constant != nil && b.constant != nil:
		return constantString(*a.constant + *b.constant)
	case a.constant == nil:
		return choice(a.condition, concat(a.then, b), concat(a.els, b))
	default:
		return choice(b.condition, concat(a, b.then), concat(a, b.els))
	}
}

func choice(condition string, then, els *stringValue) *stringValue {
	return &stringValue{condition: condition, then: then, els: els, size: then.size + els.size}
}

// concatenation lowers a CEL string expression built from constant
// strings, + and conditionals. A string symbol or any other operand is an
// error located at that operand: its value is not known, and Quint cannot
// concatenate it.
func (compiler Compiler) concatenation(e *exprpb.Expr, scope map[string]Symbol) (*stringValue, error) {
	if c := e.GetConstExpr(); c != nil {
		if s, ok := c.ConstantKind.(*exprpb.Constant_StringValue); ok {
			return constantString(s.StringValue), nil
		}
	}
	if call := e.GetCallExpr(); call != nil && call.Target == nil {
		switch {
		case call.Function == "_+_" && len(call.Args) == 2:
			a, err := compiler.concatenation(call.Args[0], scope)
			if err != nil {
				return nil, err
			}
			b, err := compiler.concatenation(call.Args[1], scope)
			if err != nil {
				return nil, err
			}
			if a.size*b.size > maxConcatenations {
				_, err := located(e, fmt.Errorf("string concatenation expands to more than %d constants", maxConcatenations))
				return nil, err
			}
			return concat(a, b), nil
		case call.Function == "_?_:_" && len(call.Args) == 3:
			condition, err := compiler.lowerCEL(call.Args[0], scope)
			if err != nil {
				return nil, err
			}
			if condition.Type != "bool" {
				_, err := located(call.Args[0], fmt.Errorf("CEL conditional type mismatch"))
				return nil, err
			}
			then, err := compiler.concatenation(call.Args[1], scope)
			if err != nil {
				return nil, err
			}
			els, err := compiler.concatenation(call.Args[2], scope)
			if err != nil {
				return nil, err
			}
			return choice(condition.Quint, then, els), nil
		}
	}
	_, err := located(e, fmt.Errorf("unsupported string concatenation: each operand must be a constant string or a conditional over them, as Quint has no string operations"))
	return nil, err
}

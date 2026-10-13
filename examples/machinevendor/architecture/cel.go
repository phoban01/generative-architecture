package architecture

import (
	"fmt"
	"generative-architecture/pkg/expr"
	"regexp"
)

type Symbol = expr.Symbol
type Evaluator = expr.Evaluator

var NewEvaluator = expr.NewEvaluator
var domainExpressions = expr.Compiler{Records: map[string]map[string]string{
	"Assignment": {"machineId": "string", "epoch": "int"},
	"Event":      {"name": "string", "host": "string", "infra": "string", "accepted": "bool"},
}}

func CompileCEL(source Expression, scope map[string]Symbol, expected string) (Symbol, error) {
	return domainExpressions.Compile(source, scope, expected)
}
func QuintType(t string) (string, error) { return domainExpressions.QuintType(t) }
func constants() map[string]Symbol {
	return map[string]Symbol{"HOSTS": {Type: "domain(string)", Quint: "HOSTS"}, "INFRAS": {Type: "domain(string)", Quint: "INFRAS"}, "MAX_EPOCH": {Type: "int", Quint: "MAX_EPOCH"}, "EMPTY_ASSIGNMENT": {Type: "Assignment", Quint: "EMPTY_ASSIGNMENT"}}
}
func copyScope(s map[string]Symbol) map[string]Symbol {
	o := map[string]Symbol{}
	for k, v := range s {
		o[k] = v
	}
	return o
}

var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
var mapType = regexp.MustCompile(`^map\(string, (string|int|bool|Assignment)\)$`)

func mapElement(t string) (string, error) {
	m := mapType.FindStringSubmatch(t)
	if m == nil {
		return "", fmt.Errorf("expected map state type: %s", t)
	}
	return m[1], nil
}

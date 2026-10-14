// Package summaries is the built-in library of client effect semantics
// (ARCH-7). A Summary describes every outcome one API operation can have
// (found, notFound, conflict, ...) as CEL templates over placeholders. The
// process lowering binds the placeholders to model state when it expands a
// call node, so the library holds no project or domain vocabulary.
package summaries

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"generative-architecture/pkg/ir"
)

// Error classes an outcome may report. An empty Outcome.Error means success.
const (
	ErrorNotFound      = "notFound"
	ErrorAlreadyExists = "alreadyExists"
	ErrorConflict      = "conflict"
	ErrorTransient     = "transient"
)

var (
	errorClasses = map[string]bool{ErrorNotFound: true, ErrorAlreadyExists: true, ErrorConflict: true, ErrorTransient: true}
	// Summaries describe runtime reads and writes; schema-only effects have
	// no outcomes to summarize.
	consistencies = map[string]bool{ir.ConsistencyCached: true, ir.ConsistencyDirect: true}
	operations    = map[string]bool{ir.OpGet: true, ir.OpList: true, ir.OpCreate: true, ir.OpUpdate: true, ir.OpPatch: true, ir.OpDelete: true, ir.OpDeleteAllOf: true, ir.OpApply: true}
	placeholder   = regexp.MustCompile(`\$[A-Za-z][A-Za-z0-9_]*`)
	subresource   = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
)

// Outcome is one possible result of an operation. Guard and the Updates
// values are CEL templates; Updates keys are the targets they assign.
// Placeholders are written $name (see data/README.md).
type Outcome struct {
	Name    string            `yaml:"name"`
	Guard   string            `yaml:"guard"`
	Updates map[string]string `yaml:"updates,omitempty"`
	Error   string            `yaml:"error,omitempty"`
}

// Summary lists the outcomes of one operation at one read consistency.
// Bindings names every placeholder the templates use; a caller must bind
// each one before the outcomes can be expanded.
type Summary struct {
	Operation   string    `yaml:"operation"`
	Consistency string    `yaml:"consistency"`
	Outcomes    []Outcome `yaml:"outcomes"`
	Bindings    []string  `yaml:"bindings"`
}

// OperationKey names the operation of an effect the way summaries key it:
// the bare operation, or subresource.operation such as status.update.
func OperationKey(operation, subresource string) string {
	if subresource == "" {
		return operation
	}
	return subresource + "." + operation
}

// Placeholders returns the sorted placeholders the templates reference.
func Placeholders(templates ...string) []string {
	seen := map[string]bool{}
	for _, t := range templates {
		for _, p := range placeholder.FindAllString(t, -1) {
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Substitute replaces every placeholder in a template with its binding. It
// is textual and does not parse the result; the receiving compiler checks
// the CEL. An unbound placeholder is an error.
func Substitute(template string, bindings map[string]string) (string, error) {
	var missing []string
	out := placeholder.ReplaceAllStringFunc(template, func(p string) string {
		b, ok := bindings[p]
		if !ok {
			missing = append(missing, p)
			return p
		}
		return b
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unbound placeholder %s in %q", strings.Join(missing, ", "), template)
	}
	return out, nil
}

// Validate checks the outcome's name, guard and error class.
func (o Outcome) Validate() error {
	if o.Name == "" {
		return fmt.Errorf("outcome name is required")
	}
	if o.Guard == "" {
		return fmt.Errorf("outcome %s: guard is required", o.Name)
	}
	if o.Error != "" && !errorClasses[o.Error] {
		return fmt.Errorf("outcome %s: unknown error %q; expected notFound, alreadyExists, conflict or transient", o.Name, o.Error)
	}
	return nil
}

// Instantiate substitutes bindings into the guard and updates, including
// the update targets.
func (o Outcome) Instantiate(bindings map[string]string) (Outcome, error) {
	out := Outcome{Name: o.Name, Error: o.Error}
	var err error
	if out.Guard, err = Substitute(o.Guard, bindings); err != nil {
		return out, fmt.Errorf("outcome %s: %w", o.Name, err)
	}
	if len(o.Updates) > 0 {
		out.Updates = make(map[string]string, len(o.Updates))
	}
	for target, value := range o.Updates {
		t, err := Substitute(target, bindings)
		if err != nil {
			return out, fmt.Errorf("outcome %s: %w", o.Name, err)
		}
		if _, dup := out.Updates[t]; dup {
			return out, fmt.Errorf("outcome %s: targets alias %s", o.Name, t)
		}
		if out.Updates[t], err = Substitute(value, bindings); err != nil {
			return out, fmt.Errorf("outcome %s: %w", o.Name, err)
		}
	}
	return out, nil
}

// Placeholders returns every placeholder used by the summary's outcomes.
func (s Summary) Placeholders() []string {
	var templates []string
	for _, o := range s.Outcomes {
		templates = append(templates, o.Guard)
		for k, v := range o.Updates {
			templates = append(templates, k, v)
		}
	}
	return Placeholders(templates...)
}

// Validate checks the operation, consistency, outcomes and that every
// placeholder the templates use is declared in Bindings.
func (s Summary) Validate() error {
	op, sub := s.Operation, ""
	if i := strings.LastIndex(op, "."); i >= 0 {
		sub, op = s.Operation[:i], s.Operation[i+1:]
		if !subresource.MatchString(sub) {
			return fmt.Errorf("summary %s: invalid subresource %q", s.Operation, sub)
		}
	}
	if !operations[op] {
		return fmt.Errorf("summary %s: unknown operation %q; expected get, list, create, update, patch, delete, deleteAllOf or apply", s.Operation, op)
	}
	if !consistencies[s.Consistency] {
		return fmt.Errorf("summary %s: unknown consistency %q; expected cached or direct", s.Operation, s.Consistency)
	}
	if len(s.Outcomes) == 0 {
		return fmt.Errorf("summary %s/%s: at least one outcome is required", s.Operation, s.Consistency)
	}
	names := map[string]bool{}
	for _, o := range s.Outcomes {
		if err := o.Validate(); err != nil {
			return fmt.Errorf("summary %s/%s: %w", s.Operation, s.Consistency, err)
		}
		if names[o.Name] {
			return fmt.Errorf("summary %s/%s: outcome %s declared twice", s.Operation, s.Consistency, o.Name)
		}
		names[o.Name] = true
	}
	declared := map[string]bool{}
	for _, b := range s.Bindings {
		if placeholder.FindString(b) != b {
			return fmt.Errorf("summary %s/%s: binding %q is not a $placeholder", s.Operation, s.Consistency, b)
		}
		declared[b] = true
	}
	for _, p := range s.Placeholders() {
		if !declared[p] {
			return fmt.Errorf("summary %s/%s: placeholder %s is used but not declared in bindings", s.Operation, s.Consistency, p)
		}
	}
	return nil
}

package goflow

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/report"
)

// Diagnostic is an extraction failure located in the source. Construct is a
// short name for the unsupported construct and Reason explains it; Source is
// the offending code when a single node is responsible.
type Diagnostic struct {
	File      string
	Line      int
	Construct string
	Reason    string
	Source    string
	// pos orders diagnostics of one file in source order.
	pos token.Pos
}

// Diagnostics is every located failure of one extraction, in source
// order. It unwraps to each Diagnostic, so AsDiagnostic finds the first.
type Diagnostics []*Diagnostic

func (ds Diagnostics) Error() string {
	lines := make([]string, len(ds))
	for i, d := range ds {
		lines[i] = d.Error()
	}
	return strings.Join(lines, "\n")
}

// Unwrap returns each diagnostic as an error.
func (ds Diagnostics) Unwrap() []error {
	out := make([]error, len(ds))
	for i, d := range ds {
		out[i] = d
	}
	return out
}

// AsDiagnostics returns every diagnostic err holds: the list of a
// Diagnostics it is or wraps, else the one Diagnostic it is or wraps. It
// reports false when err holds none.
func AsDiagnostics(err error) ([]*Diagnostic, bool) {
	var ds Diagnostics
	if errors.As(err, &ds) && len(ds) > 0 {
		return ds, true
	}
	if d, ok := AsDiagnostic(err); ok {
		return []*Diagnostic{d}, true
	}
	return nil, false
}

// diagnosticsError returns ds as an error: nil for none, the Diagnostic
// itself for one, else the list sorted into source order.
func diagnosticsError(ds []*Diagnostic) error {
	switch len(ds) {
	case 0:
		return nil
	case 1:
		return ds[0]
	}
	sorted := slices.Clone(ds)
	slices.SortStableFunc(sorted, func(a, b *Diagnostic) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		if c := a.Line - b.Line; c != 0 {
			return c
		}
		return int(a.pos) - int(b.pos)
	})
	return Diagnostics(sorted)
}

func (d *Diagnostic) Error() string {
	where := d.File
	if d.Line > 0 {
		if where != "" {
			where += ":"
		}
		where += fmt.Sprintf("%d", d.Line)
	}
	msg := d.Reason
	if d.Source != "" {
		msg += ": " + d.Source
	}
	if where == "" {
		return msg
	}
	return where + ": " + msg
}

// Item converts the diagnostic into a support-report entry.
func (d *Diagnostic) Item() report.Item {
	return report.Item{
		Kind:      d.Construct,
		Construct: d.Source,
		Reason:    d.Reason,
		Location:  report.Location{File: d.File, Line: d.Line},
	}
}

// AsDiagnostic reports whether err is, or wraps, a *Diagnostic.
func AsDiagnostic(err error) (*Diagnostic, bool) {
	var d *Diagnostic
	if errors.As(err, &d) {
		return d, true
	}
	return nil, false
}

// diagnose builds a Diagnostic for the Go node n.
func (e *extractor) diagnose(n ast.Node, construct, reason string) *Diagnostic {
	d := &Diagnostic{Construct: construct, Reason: reason, File: e.opts.File}
	if n != nil {
		d.Source = Code(n)
		d.pos = n.Pos()
		if e.fset != nil {
			p := e.fset.Position(n.Pos())
			d.Line = p.Line
			if p.Filename != "" {
				d.File = p.Filename
			}
		}
	}
	return d
}

// unsupported locates a failure to compile the CEL lowered from the Go
// expression n. A diagnostic passes through unchanged.
func (e *extractor) unsupported(n ast.Node, err error) error {
	if _, ok := AsDiagnostic(err); ok {
		return err
	}
	return e.diagnose(n, "expression", firstLine(err))
}

// firstLine returns the first line of an error message, so a support
// report row stays on one line.
func firstLine(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return msg
}

// NewDiagnostic builds a Diagnostic from a position in fset; frontends that
// walk their own syntax trees use it to report unsupported constructs.
func NewDiagnostic(fset *token.FileSet, n ast.Node, construct, reason string) *Diagnostic {
	e := &extractor{fset: fset}
	return e.diagnose(n, construct, reason)
}

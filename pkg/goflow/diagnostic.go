package goflow

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"

	"generative-architecture/pkg/report"
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

// NewDiagnostic builds a Diagnostic from a position in fset; frontends that
// walk their own syntax trees use it to report unsupported constructs.
func NewDiagnostic(fset *token.FileSet, n ast.Node, construct, reason string) *Diagnostic {
	e := &extractor{fset: fset}
	return e.diagnose(n, construct, reason)
}

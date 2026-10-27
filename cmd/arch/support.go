package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/phoban01/generative-architecture/pkg/ir"
	"github.com/phoban01/generative-architecture/pkg/project"
	"github.com/phoban01/generative-architecture/pkg/report"
)

// errStrict marks a run that verification mode (--strict) refused because
// the selected packages hold unsupported constructs or unresolved facts.
var errStrict = errors.New("verification mode (--strict) refuses incomplete semantics")

// runSupport extracts the project in memory and prints its support report
// as a table: included, summarized, unsupported and unresolved items with
// their locations. It writes nothing. In inventory mode it succeeds
// whatever the report holds; with --strict it fails on unsupported or
// unresolved items in the selected packages.
func runSupport(g globals, args []string) error {
	fs := flag.NewFlagSet("support", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("arch support: unexpected arguments %v", fs.Args())
	}
	p, err := project.Load(g.project)
	if err != nil {
		return fmt.Errorf("arch support: %w", err)
	}
	doc, support, err := extract(p)
	if err != nil {
		return fmt.Errorf("arch support: %w", err)
	}
	support.Unresolved = append(support.Unresolved, unresolvedFacts(doc)...)
	support.Sort()
	if err := printSupport(g.stdout, *support); err != nil {
		return err
	}
	if err := enforce(g, p, *support); err != nil {
		return fmt.Errorf("arch support: %w", err)
	}
	return nil
}

// printSupport writes one row per item, bucket by bucket.
func printSupport(w io.Writer, s report.Support) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tKIND\tCONSTRUCT\tLOCATION\tREASON")
	for _, b := range []struct {
		name  string
		items []report.Item
	}{{report.Included, s.Included}, {report.Summarized, s.Summarized}, {report.Unsupported, s.Unsupported}, {report.Unresolved, s.Unresolved}} {
		for _, it := range b.items {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", b.name, it.Kind, it.Construct, location(it.Location), it.Reason)
		}
	}
	return tw.Flush()
}

// location renders file:line, the file alone without a line and "-" when
// nothing is known.
func location(l report.Location) string {
	switch {
	case l.File == "":
		return "-"
	case l.Line == 0:
		return l.File
	}
	return l.File + ":" + strconv.Itoa(l.Line)
}

// unresolvedFacts lists the IR facts whose evidence is unresolved.
func unresolvedFacts(doc ir.Document) []report.Item {
	var out []report.Item
	at := func(p ir.Provenance) report.Location {
		return report.Location{File: p.Source.File, Line: p.Source.Line}
	}
	for _, w := range doc.Watches {
		if w.Evidence == ir.Unresolved {
			out = append(out, report.Item{Kind: "watch", Construct: w.ID, Reason: "evidence is unresolved", Location: at(w.Provenance)})
		}
	}
	for _, e := range doc.Effects {
		if e.Evidence == ir.Unresolved {
			out = append(out, report.Item{Kind: "effect", Construct: e.ID, Reason: "evidence is unresolved", Location: at(e.Provenance)})
		}
	}
	for _, c := range doc.Contracts {
		if c.Evidence == ir.Unresolved {
			out = append(out, report.Item{Kind: "contract", Construct: c.ID(), Reason: "evidence is unresolved", Location: at(c.Provenance)})
		}
	}
	return out
}

// inScope accepts a location in one of the project's selected packages,
// and an unknown location, so that nothing escapes verification mode.
func inScope(p project.Project) func(report.Location) bool {
	return func(l report.Location) bool { return l.File == "" || p.Spec.Selects(l.File) }
}

// enforce applies the mode to a report. With --strict any unsupported or
// unresolved item in scope is an error; in inventory mode they are counted
// on stderr.
func enforce(g globals, p project.Project, s report.Support) error {
	items := s.Blocking(inScope(p))
	if len(items) == 0 {
		return nil
	}
	first := items[0]
	if g.strict {
		return fmt.Errorf("%w: %d unsupported or unresolved items in the selected packages, first %s %s at %s", errStrict, len(items), first.Kind, first.Construct, location(first.Location))
	}
	fmt.Fprintf(g.stderr, "inventory mode: %d unsupported or unresolved items in the selected packages; --strict fails on them\n", len(items))
	return nil
}

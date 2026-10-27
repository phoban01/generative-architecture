package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

// errDiffers reports that two IR documents differ. arch exits with status 2
// for it, as diff(1) does, so scripts can tell a difference from a failure.
var errDiffers = exitError{code: 2, msg: "arch diff: documents differ"}

// runDiff compares two IR documents and prints the added, removed and
// changed records. With --json it prints the delta as JSON.
func runDiff(g globals, args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	asJSON := fs.Bool("json", false, "print the delta as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: arch diff [--json] <old.yaml> <new.yaml>")
	}
	old, err := ir.Read(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("arch diff: %w", err)
	}
	new, err := ir.Read(fs.Arg(1))
	if err != nil {
		return fmt.Errorf("arch diff: %w", err)
	}
	d := ir.Diff(old, new)
	if *asJSON {
		// Print empty lists as [] rather than null.
		for _, list := range []*[]ir.Change{&d.Added, &d.Removed, &d.Changed} {
			if *list == nil {
				*list = []ir.Change{}
			}
		}
		enc := json.NewEncoder(g.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(d); err != nil {
			return err
		}
	} else {
		printDelta(g.stdout, d)
	}
	if !d.Empty() {
		return errDiffers
	}
	return nil
}

// printDelta writes one line per record: + added, - removed, ~ changed with
// the changed fields in parentheses.
func printDelta(w io.Writer, d ir.Delta) {
	for _, c := range d.Added {
		fmt.Fprintf(w, "+ %s %s\n", c.Kind, c.ID)
	}
	for _, c := range d.Removed {
		fmt.Fprintf(w, "- %s %s\n", c.Kind, c.ID)
	}
	for _, c := range d.Changed {
		fmt.Fprintf(w, "~ %s %s (%s)\n", c.Kind, c.ID, strings.Join(c.Fields, ", "))
	}
}

// Command arch is the generic command-line entry point for the library. It
// operates on a project document and imports nothing from examples/.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

// globals are the flags shared by every subcommand.
type globals struct {
	project string
	out     string
	// strict selects verification mode: unsupported constructs and
	// unresolved facts in the selected packages fail the run. Without it
	// arch runs in inventory mode and reports them (EVID-3).
	strict bool
	stdout io.Writer
	stderr io.Writer
}

// exitError ends arch with a given status. A nonzero status other than 1
// carries a result, not a failure, so main prints no message for it.
type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

// command is a subcommand entry point. It receives the global options and
// the remaining arguments after the subcommand name.
type command struct {
	name    string
	summary string
	run     func(g globals, args []string) error
}

var commands = []command{
	{"init", "write a starter project document", runInit},
	{"extract", "extract behaviour, effects and watches from Go source", runExtract},
	{"generate", "generate models and workspaces from the extracted IR", runGenerate},
	{"render", "build the LikeC4 site for a generated workspace", runRender},
	{"serve", "serve the rendered LikeC4 site on localhost", runServe},
	{"check", "sample the generated model with Quint and check its properties", runCheck},
	{"replay", "replay saved traces against the generated model", runReplay},
	{"diff", "compare generated artifacts with the committed ones", runDiff},
	{"support", "report included, summarized and unsupported constructs", runSupport},
}

func usage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintln(w, "usage: arch [--project <path>] [--out <dir>] [--strict] <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	names := make([]command, len(commands))
	copy(names, commands)
	sort.Slice(names, func(i, j int) bool { return names[i].name < names[j].name })
	for _, c := range names {
		fmt.Fprintf(w, "  %-9s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "flags:")
	fs.SetOutput(w)
	fs.PrintDefaults()
}

// run parses args (without the program name) and dispatches. It returns
// flag.ErrHelp when usage was printed because no command was given.
func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("arch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	g := globals{stdout: stdout, stderr: stderr}
	fs.StringVar(&g.project, "project", "project.yaml", "project document to operate on")
	fs.StringVar(&g.out, "out", "generated", "directory for generated artifacts")
	fs.BoolVar(&g.strict, "strict", false, "verification mode: fail on unsupported constructs and unresolved facts in the selected packages (extract, generate, check, support)")
	fs.Usage = func() { usage(stderr, fs) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		usage(stdout, fs)
		return flag.ErrHelp
	}
	for _, c := range commands {
		if c.name == rest[0] {
			return c.run(g, rest[1:])
		}
	}
	return fmt.Errorf("unknown command %q; run arch without arguments for usage", rest[0])
}

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return
	}
	var exit exitError
	if errors.As(err, &exit) {
		os.Exit(exit.code)
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// arch is a native Go compiler and model-based test driver for YAML/CEL contracts.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"generative-architecture/examples/machinevendor/architecture"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "arch:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: arch <extract|generate|check|render|serve> [options]\n  extract   extract Go types and architecture markers\n  generate  extract sources, then compile YAML/CEL into Quint, LikeC4 and metadata\n  check     generate fresh Quint traces, evaluate CEL and replay Go simulator\n  check --replay  validate saved trace evidence using only Go\n  render    validate/build diagrams with external LikeC4\n  serve     serve the existing static diagram on localhost")
		return nil
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	root := fs.String("root", ".", "project directory")
	update := fs.Bool("update-lock", false, "reviewed contract hash update (generate only)")
	replay := fs.Bool("replay", false, "replay existing hash-checked traces (check only)")
	quint := fs.String("quint", "", "Quint executable; defaults to QUINT_BIN or quint on PATH")
	likec4 := fs.String("likec4", "", "LikeC4 executable; defaults to LIKEC4_BIN or likec4 on PATH")
	addr := fs.String("addr", "127.0.0.1:8767", "serve listen address")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if args[0] == "serve" {
		dir := filepath.Join(*root, "site")
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
			return fmt.Errorf("build the diagram first: %w", err)
		}
		fmt.Printf("Serving saved diagram at http://%s\n", *addr)
		server := &http.Server{Addr: *addr, Handler: http.FileServer(http.Dir(dir)), ReadHeaderTimeout: 5 * time.Second}
		return server.ListenAndServe()
	}
	if args[0] != "extract" && args[0] != "generate" && args[0] != "check" && args[0] != "render" {
		return fmt.Errorf("unknown command %s", args[0])
	}
	if args[0] == "extract" || args[0] == "generate" {
		if err := architecture.ExtractSources(*root); err != nil {
			return err
		}
		if args[0] == "extract" {
			fmt.Println("Extracted Go structure and declared architecture mappings")
			return nil
		}
	}
	p, err := architecture.Load(*root)
	if err != nil {
		return err
	}
	m, err := p.Compile()
	if err != nil {
		return err
	}
	switch args[0] {
	case "generate":
		if err := m.Generate(*update); err != nil {
			return err
		}
		fmt.Printf("Generated %d components, %d bindings and %d actions\n", len(p.System.Components), len(m.Edges), len(m.Actions))
	case "check":
		_, err = m.Check(architecture.CheckOptions{Replay: *replay, Quint: *quint})
	case "render":
		err = m.Render(*likec4)
	}
	return err
}

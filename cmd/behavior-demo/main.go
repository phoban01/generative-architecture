package main

import (
	"flag"
	"fmt"
	"generative-architecture/examples/machinevendor/behaviorgen"
	"os"
)

func main() {
	root := flag.String("root", ".", "project directory")
	quint := flag.String("quint", "", "Quint executable (or QUINT_BIN)")
	generate := flag.Bool("generate-only", false, "extract source and generate models without running Quint")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	var err error
	if *generate {
		_, _, err = behaviorgen.Generate(*root)
	} else {
		_, err = behaviorgen.Check(*root, *quint)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "behavior-demo:", err)
		os.Exit(1)
	}
}

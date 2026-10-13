package main

import (
	"flag"
	"fmt"
	"generative-architecture/examples/machinevendor/runtimecheck"
	"os"
)

func main() {
	root := flag.String("root", ".", "project directory")
	tool := flag.String("quint", "", "Quint executable")
	generate := flag.Bool("generate-only", false, "only generate composed model")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	var err error
	if *generate {
		_, err = runtimecheck.GenerateComposition(*root)
	} else {
		_, err = runtimecheck.CheckComposition(*root, *tool)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "composition-demo:", err)
		os.Exit(1)
	}
}

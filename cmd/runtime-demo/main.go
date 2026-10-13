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
	generate := flag.Bool("generate-only", false, "generate without checking")
	flag.Parse()
	var err error
	if *generate {
		_, err = runtimecheck.Generate(*root)
	} else {
		_, err = runtimecheck.Check(*root, *tool)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime-demo:", err)
		os.Exit(1)
	}
}

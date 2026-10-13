package main

import (
	"flag"
	"fmt"
	"generative-architecture/examples/machinevendor/controllerdemo"
	"os"
)

func main() {
	root := flag.String("root", ".", "project directory")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: controller-demo [-root directory]")
		os.Exit(2)
	}
	if _, err := controllerdemo.Check(*root); err != nil {
		fmt.Fprintln(os.Stderr, "controller-demo:", err)
		os.Exit(1)
	}
}

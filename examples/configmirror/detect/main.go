// Command detect replays ConfigMirror's committed evidence through the
// implementation it was compiled with and prints, one per line, what
// detected a divergence (see configmirror.Detect). configmirror.RunMutations
// compiles it with a mutated file overlaid; an unmutated build prints
// nothing.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"generative-architecture/examples/configmirror"
)

func main() {
	root := flag.String("root", ".", "example directory")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	detected, err := configmirror.Detect(ctx, *root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, d := range detected {
		fmt.Println(d)
	}
}

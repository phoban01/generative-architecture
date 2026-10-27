package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/phoban01/generative-architecture/pkg/likec4"
)

// siteDir is the default build output under --out.
const siteDir = "site"

// renderTimeout bounds one LikeC4 build.
const renderTimeout = 5 * time.Minute

// runRender builds the static site of the LikeC4 workspace arch generate
// wrote under <out>/likec4. The LikeC4 CLI comes from LIKEC4_BIN or PATH
// and must lie in the supported version range.
func runRender(g globals, args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	site := fs.String("site", "", "output directory (default <out>/site)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("arch render: unexpected arguments %v", fs.Args())
	}
	if *site == "" {
		*site = filepath.Join(g.out, siteDir)
	}
	workspace := filepath.Join(g.out, likec4Dir)
	if _, err := os.Stat(filepath.Join(workspace, "model.c4")); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("arch render: no workspace in %s; run arch generate first", workspace)
	}
	ctx, cancel := context.WithTimeout(context.Background(), renderTimeout)
	defer cancel()
	version, err := likec4.CLI{}.Build(ctx, workspace, *site)
	if err != nil {
		return fmt.Errorf("arch render: %w", err)
	}
	fmt.Fprintf(g.stdout, "rendered %s into %s with LikeC4 %s\n", workspace, *site, version)
	return nil
}

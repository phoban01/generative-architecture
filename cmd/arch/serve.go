package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// runServe serves the site arch render built, as static files, on
// localhost. It runs until interrupted.
func runServe(g globals, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	site := fs.String("site", "", "directory to serve (default <out>/site)")
	port := fs.Int("port", 8080, "TCP port on localhost")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("arch serve: unexpected arguments %v", fs.Args())
	}
	if *port <= 0 || *port > 65535 {
		return fmt.Errorf("arch serve: invalid port %d", *port)
	}
	if *site == "" {
		*site = filepath.Join(g.out, siteDir)
	}
	h, err := siteHandler(*site)
	if err != nil {
		return fmt.Errorf("arch serve: %w", err)
	}
	addr := net.JoinHostPort("localhost", strconv.Itoa(*port))
	fmt.Fprintf(g.stdout, "serving %s at http://%s/\n", *site, addr)
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

// siteHandler serves the files of a built site. The site must hold an
// index.html.
func siteHandler(dir string) (http.Handler, error) {
	if _, err := os.Stat(filepath.Join(dir, "index.html")); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no index.html in %s; run arch render first", dir)
	} else if err != nil {
		return nil, err
	}
	return http.FileServer(http.Dir(dir)), nil
}

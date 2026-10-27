package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// implemented lists the subcommands that no longer report not implemented.
var implemented = map[string]bool{"extract": true, "init": true, "diff": true, "generate": true, "support": true, "check": true, "replay": true}

func TestUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"nope"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}

func TestUsageWithoutCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run(nil, &out, &errOut)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected ErrHelp, got %v", err)
	}
	for _, c := range commands {
		if !strings.Contains(out.String(), c.name) {
			t.Fatalf("usage does not list %q:\n%s", c.name, out.String())
		}
	}
}

func TestSubcommandsReportNotImplemented(t *testing.T) {
	for _, c := range commands {
		if implemented[c.name] {
			continue
		}
		var out, errOut bytes.Buffer
		err := run([]string{"--project", "p.yaml", "--out", "o", c.name}, &out, &errOut)
		if !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "not implemented") {
			t.Fatalf("%s: expected ErrUnsupported, got %v", c.name, err)
		}
	}
}

// TestImportsNothingFromExamples keeps the CLI generic (ADOPT-5).
func TestImportsNothingFromExamples(t *testing.T) {
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps}, ".")
	if err != nil {
		t.Fatal(err)
	}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if strings.HasPrefix(p.PkgPath, "generative-architecture/examples/") {
			t.Errorf("cmd/arch depends on %s", p.PkgPath)
		}
	})
}

package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

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
		var out, errOut bytes.Buffer
		err := run([]string{"--project", "p.yaml", "--out", "o", c.name}, &out, &errOut)
		if !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "not implemented") {
			t.Fatalf("%s: expected ErrUnsupported, got %v", c.name, err)
		}
	}
}

package likec4

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportedVersionRange(t *testing.T) {
	for _, tc := range []struct {
		version string
		cli     CLI
		ok      bool
	}{
		{"1.59.4", CLI{}, true},
		{"1.59.0", CLI{}, true},
		{"v1.59.9\n", CLI{}, true},
		{"1.58.9", CLI{}, false},
		{"1.60.0", CLI{}, false},
		{"1.59.5-rc.1", CLI{}, false},
		{"latest", CLI{}, false},
		{"1.60.1", CLI{MaxVersion: "1.61.0"}, true},
		{"1.59.4", CLI{MinVersion: "nope"}, false},
	} {
		if err := tc.cli.Supported(tc.version); (err == nil) != tc.ok {
			t.Errorf("Supported(%q) with %+v: %v", tc.version, tc.cli, err)
		}
	}
}

// fakeCLI writes an executable that prints version and records its
// arguments in args.txt beside it.
func fakeCLI(t *testing.T, version string) (CLI, string) {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo " + version + "; exit 0; fi\necho \"$@\" > " + args + "\n"
	bin := filepath.Join(dir, "likec4")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return CLI{Executable: bin}, args
}

func TestBuildChecksVersionFirst(t *testing.T) {
	cli, args := fakeCLI(t, "1.70.0")
	version, err := cli.Build(context.Background(), "ws", "site")
	if err == nil || !strings.Contains(err.Error(), "unsupported LikeC4 version") || version != "1.70.0" {
		t.Fatalf("got %q %v", version, err)
	}
	if _, err := os.Stat(args); !os.IsNotExist(err) {
		t.Fatal("build ran with an unsupported version")
	}
	cli, args = fakeCLI(t, "1.59.4")
	if _, err := cli.Build(context.Background(), "ws", "site"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "build ws --output site --base ./ --use-hash-history --output-single-file" {
		t.Fatalf("arguments %q", got)
	}
}

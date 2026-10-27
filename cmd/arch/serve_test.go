package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeBuiltSite(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<html>views</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := siteHandler(site)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(b) != "<html>views</html>" {
		t.Fatalf("GET / = %d %q", resp.StatusCode, b)
	}
}

func TestServeNeedsRenderedSite(t *testing.T) {
	if _, err := siteHandler(t.TempDir()); err == nil || !strings.Contains(err.Error(), "run arch render first") {
		t.Fatalf("got %v", err)
	}
	_, err := arch(t, configMirror, t.TempDir(), false, "serve", "--port", "0")
	if err == nil || !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderNeedsWorkspace(t *testing.T) {
	if _, err := arch(t, configMirror, t.TempDir(), false, "render"); err == nil || !strings.Contains(err.Error(), "run arch generate first") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderConfigMirror(t *testing.T) {
	if os.Getenv("LIKEC4_BIN") == "" {
		t.Skip("LIKEC4_BIN not set")
	}
	out, err := generateTo(t, configMirror)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arch(t, configMirror, out, false, "render"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, siteDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	// Every generated view is in the site.
	for _, view := range []string{"context", "process", "resources", "runtime"} {
		if !strings.Contains(string(b), view) {
			t.Errorf("site lacks view %s", view)
		}
	}
	if _, err := siteHandler(filepath.Join(out, siteDir)); err != nil {
		t.Fatal(err)
	}
}

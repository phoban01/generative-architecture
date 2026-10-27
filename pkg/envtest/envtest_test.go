package envtest

import (
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	for name, c := range map[string]struct {
		env        map[string]string
		skip, fail bool
	}{
		"no assets":          {env: nil, skip: true},
		"assets":             {env: map[string]string{AssetsEnv: "/assets"}},
		"assets and require": {env: map[string]string{AssetsEnv: "/assets", RequiredEnv: "1"}},
		"required, missing":  {env: map[string]string{RequiredEnv: "1"}, fail: true},
	} {
		skip, err := decide(func(k string) string { return c.env[k] })
		if skip != c.skip || (err != nil) != c.fail {
			t.Errorf("%s: skip %v, err %v", name, skip, err)
		}
		if err != nil && !strings.Contains(err.Error(), "setup-envtest") {
			t.Errorf("%s: error %q does not say how to install the assets", name, err)
		}
	}
}

func TestStartSkipsWithoutAssets(t *testing.T) {
	t.Setenv(AssetsEnv, "")
	t.Setenv(RequiredEnv, "")
	var inner *testing.T
	t.Run("start", func(t *testing.T) {
		inner = t
		Start(t, nil)
		t.Error("Start returned without assets")
	})
	if !inner.Skipped() {
		t.Fatal("Start did not skip")
	}
}

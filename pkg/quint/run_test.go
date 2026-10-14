package quint

import "testing"

func TestSupported(t *testing.T) {
	cases := []struct {
		version string
		opts    Options
		ok      bool
	}{
		{"0.33.0", Options{}, true},
		{"0.33.5", Options{}, true},
		{"v0.33.5\n", Options{}, true},
		{"0.32.9", Options{}, false},
		{"0.34.0", Options{}, false},
		{"0.34.0-rc1", Options{}, false},
		{"latest", Options{}, false},
		{"", Options{}, false},
		{"0.34.0", Options{MaxVersion: "0.35.0"}, true},
		{"0.32.9", Options{MinVersion: "0.32.0"}, true},
		{"0.33.0", Options{MinVersion: "0.33.1"}, false},
		{"0.33.0", Options{MinVersion: "nope"}, false},
	}
	for _, c := range cases {
		err := Supported(c.version, c.opts)
		if c.ok && err != nil {
			t.Errorf("Supported(%q, %+v) = %v; want nil", c.version, c.opts, err)
		}
		if !c.ok && err == nil {
			t.Errorf("Supported(%q, %+v) = nil; want error", c.version, c.opts)
		}
	}
}

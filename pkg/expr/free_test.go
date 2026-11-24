package expr

import (
	"reflect"
	"testing"
)

func TestFreeIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		source Expression
		want   []string
	}{
		{`true`, []string{}},
		{`port == "ready" && next == "consumed"`, []string{"next", "port"}},
		{`state != "done" || port == "consumed"`, []string{"port", "state"}},
		{`port.ready && !port.ready`, []string{"port"}},
		{`"state" == x`, []string{"x"}},
		{`items.all(i, i.ready && limit > 0)`, []string{"items", "limit"}},
		{`items.exists(i, i == needle) && i`, []string{"i", "items", "needle"}},
		{`[a, b].size() + {c: d}.size()`, []string{"a", "b", "c", "d"}},
		{`f(a) ? b : a`, []string{"a", "b"}}, // a called function is not state
	} {
		got, err := FreeIdentifiers(tc.source)
		if err != nil {
			t.Fatalf("%s: %v", tc.source, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("FreeIdentifiers(%s) = %v, want %v", tc.source, got, tc.want)
		}
	}
	if _, err := FreeIdentifiers(`a &&`); err == nil {
		t.Error("expected a parse error")
	}
}

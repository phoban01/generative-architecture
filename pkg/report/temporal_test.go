package report

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTemporalSection(t *testing.T) {
	v := sampleVerification()
	v.Temporal = []TemporalResult{
		{Name: "reachesThree", Status: TemporalVerified, Weak: []string{"increment"}, Backend: "quint verify (tlc)", Bound: "complete reachable state space"},
		{Name: "settles", Status: TemporalUnchecked, Reason: "no fairness assumption declared"},
	}
	v.Findings = []Finding{{Category: CategoryUnsupported, Property: "settles", Message: "temporal property settles unchecked"}}
	if got := v.Conclude(); got != Incomplete {
		t.Fatalf("an unchecked property must not pass the report, got %s", got)
	}
	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := v.Write(path); err != nil {
		t.Fatal(err)
	}
	read, err := ReadVerification(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read.Temporal, v.Temporal) {
		t.Fatalf("got %+v", read.Temporal)
	}
	var b strings.Builder
	v.WriteTemporal(&b)
	for _, want := range []string{"reachesThree: verified with quint verify (tlc) under fairness weak(increment); bound: complete reachable state space", "settles: unchecked (no fairness assumption declared)"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}

func TestTemporalSectionValidation(t *testing.T) {
	for name, r := range map[string]TemporalResult{
		"verified without fairness": {Name: "p", Status: TemporalVerified, Backend: "tlc", Bound: "all"},
		"violated without bound":    {Name: "p", Status: TemporalViolated, Weak: []string{"a"}, Backend: "tlc"},
		"unchecked without reason":  {Name: "p", Status: TemporalUnchecked},
		"passed":                    {Name: "p", Status: "passed", Reason: "sampled"},
	} {
		v := sampleVerification()
		v.Temporal = []TemporalResult{r}
		v.Conclude()
		if err := v.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

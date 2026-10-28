package summaries

import (
	"strings"
	"testing"
)

func TestObjectSummaries(t *testing.T) {
	for _, tc := range []struct {
		function   string
		controller string
	}{
		{"SetControllerReference", "true"},
		{"SetOwnerReference", "false"},
	} {
		s, ok := ObjectSummaryFor(controllerutilPackage, tc.function)
		if !ok {
			t.Fatalf("%s: no summary", tc.function)
		}
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		writes := map[string]string{}
		for _, w := range s.Writes {
			writes[strings.Join(w.Path, ".")] = w.Value
		}
		if writes["metadata.ownerReferences.name"] != "$ownerName" || writes["metadata.ownerReferences.controller"] != tc.controller {
			t.Errorf("%s: writes %v", tc.function, writes)
		}
	}
	if _, ok := ObjectSummaryFor(controllerutilPackage, "RemoveOwnerReference"); ok {
		t.Fatal("summary for an unsummarized function")
	}
	for name, s := range map[string]ObjectSummary{
		"same argument":    {Source: 1, Target: 1},
		"unbound":          {Target: 1, Writes: []FieldWrite{{[]string{"a"}, "$x"}}},
		"duplicate write":  {Target: 1, Writes: []FieldWrite{{[]string{"a"}, "1"}, {[]string{"a"}, "2"}}},
		"written unvalued": {Target: 1, Writes: []FieldWrite{{[]string{"a"}, "1"}}, Unvalued: [][]string{{"a"}}},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

package summaries

import (
	"strings"
	"testing"
	"testing/fstest"
)

const doc = `apiVersion: architecture.local/v1alpha1
kind: EffectSummaries
metadata:
  name: sample
spec:
  summaries:
    - operation: get
      consistency: direct
      bindings: [$exists, $object, $snapshot]
      outcomes:
        - name: found
          guard: $exists
          updates: {$snapshot: $object}
        - name: notFound
          guard: '!$exists'
          error: notFound
`

func TestLoadRegistry(t *testing.T) {
	fsys := fstest.MapFS{
		"lib/get.yaml":   {Data: []byte(doc)},
		"lib/README.md":  {Data: []byte("not yaml")},
		"lib/sub/x.yaml": {Data: []byte("ignored: subdirectory")},
	}
	r, err := Load(fsys, "lib")
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Lookup("get", "direct")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Outcomes) != 2 || s.Outcomes[0].Updates["$snapshot"] != "$object" || s.Outcomes[1].Error != ErrorNotFound {
		t.Fatalf("unexpected summary %+v", s)
	}
	if _, err := r.Lookup("get", "cached"); err == nil || !strings.Contains(err.Error(), `no summary for operation "get" at consistency "cached"`) {
		t.Fatalf("missing lookup: %v", err)
	}
	if _, err := r.Lookup("deleteAllOf", "direct"); err == nil || !strings.Contains(err.Error(), "get/direct") {
		t.Fatalf("missing lookup should list known keys: %v", err)
	}
}

func TestBuiltinLoadsEmbeddedData(t *testing.T) {
	r := Builtin()
	for _, k := range r.Keys() {
		op, consistency, _ := strings.Cut(k, "/")
		if _, err := r.Lookup(op, consistency); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Lookup("missing", "direct"); err == nil || !strings.Contains(err.Error(), `no summary for operation "missing"`) {
		t.Fatalf("missing operation: %v", err)
	}
}

func TestLoadRejectsBadDocuments(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"wrong kind":       {strings.Replace(doc, "EffectSummaries", "TransitionModel", 1), "expected architecture.local/v1alpha1 EffectSummaries"},
		"unknown field":    {strings.Replace(doc, "error: notFound", "error: notFound\n          retry: true", 1), "field retry not found"},
		"status payload":   {strings.Replace(doc, "spec:", "status:", 1), "only spec"},
		"unknown error":    {strings.Replace(doc, "error: notFound", "error: gone", 1), "unknown error"},
		"undeclared":       {strings.Replace(doc, "bindings: [$exists, $object, $snapshot]", "bindings: [$exists, $object]", 1), "placeholder $snapshot is used but not declared"},
		"bad binding":      {strings.Replace(doc, "$snapshot]", "snapshot]", 1), "not a $placeholder"},
		"duplicate name":   {strings.Replace(doc, "name: notFound", "name: found", 1), "declared twice"},
		"missing guard":    {strings.Replace(doc, "          guard: $exists\n", "", 1), "guard is required"},
		"bad operation":    {strings.Replace(doc, "operation: get", "operation: fetch", 1), "unknown operation"},
		"bad subresource":  {strings.Replace(doc, "operation: get", "operation: Status.get", 1), "invalid subresource"},
		"bad consistency":  {strings.Replace(doc, "consistency: direct", "consistency: schema", 1), "unknown consistency"},
		"duplicate":        {strings.Replace(doc, "summaries:\n", "summaries:\n    - {operation: get, consistency: direct, bindings: [], outcomes: [{name: x, guard: 'true'}]}\n", 1), "registered twice"},
		"cel must be text": {strings.Replace(doc, "guard: $exists", "guard: [1]", 1), "cannot unmarshal"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(fstest.MapFS{"d/x.yaml": {Data: []byte(c.body)}}, "d")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want error containing %q", err, c.want)
			}
		})
	}
}

func TestSubstituteAndInstantiate(t *testing.T) {
	o := Outcome{Name: "found", Guard: "$exists && $options.namespace == \"$literal\"", Updates: map[string]string{"$snapshot": "$object"}}
	got, err := o.Instantiate(map[string]string{"$exists": "db1_exists", "$options": "opts", "$snapshot": "local", "$object": "db1", "$literal": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Guard != `db1_exists && opts.namespace == "x"` || got.Updates["local"] != "db1" || len(got.Updates) != 1 {
		t.Fatalf("unexpected instantiation %+v", got)
	}
	if _, err := o.Instantiate(map[string]string{"$exists": "e"}); err == nil || !strings.Contains(err.Error(), "unbound placeholder $options") {
		t.Fatalf("unbound: %v", err)
	}
	if _, err := (Outcome{Name: "x", Guard: "true", Updates: map[string]string{"$a": "1", "$b": "2"}}).Instantiate(map[string]string{"$a": "v", "$b": "v"}); err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("aliased targets: %v", err)
	}
	if p := Placeholders("$b + $a", "$a"); len(p) != 2 || p[0] != "$a" || p[1] != "$b" {
		t.Fatalf("placeholders %v", p)
	}
	if OperationKey("update", "status") != "status.update" || OperationKey("get", "") != "get" {
		t.Fatal("operation key")
	}
}

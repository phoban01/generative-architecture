package likec4

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/phoban01/generative-architecture/pkg/ir"
)

// hinted has three elements, two relations, a static view and a dynamic
// view.
func hinted() Workspace {
	w := twoElements()
	if err := w.Add([]Element{{ID: "queue", Kind: KindQueue, Title: "Work queue", Evidence: ir.Declared}},
		[]Relation{{From: "queue", To: "mirror", Label: "delivers", Evidence: ir.Declared}},
		View{Name: "flow", Title: "Flow", Dynamic: true, Steps: []Step{{From: "queue", To: "mirror", Label: "delivers"}}}); err != nil {
		panic(err)
	}
	w.Views[0].Include = append(w.Views[0].Include, "queue")
	return w
}

const hintsYAML = `apiVersion: architecture.local/v1alpha1
kind: ViewHints
metadata:
  name: overview
spec:
  titles:
    mirror: Mirror
    overview: Everything
  groups:
    Control plane:
      - mirror
      - queue
  layout:
    overview: LeftRight
    flow: TopBottom
`

func writeHints(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hints.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// shape is what hints must not change: every element without its title,
// every relation, and every view's elements and steps.
type shape struct {
	Elements  []Element
	Relations []Relation
	Views     map[string][]string
}

func shapeOf(w Workspace) shape {
	s := shape{Relations: append([]Relation(nil), w.Relations...), Views: map[string][]string{}}
	for _, e := range w.Elements {
		e.Title = ""
		s.Elements = append(s.Elements, e)
	}
	for _, v := range w.Views {
		include := append([]string(nil), v.Include...)
		sort.Strings(include)
		for _, st := range v.Steps {
			include = append(include, st.From+"->"+st.To+":"+st.Label)
		}
		s.Views[v.Name] = include
	}
	return s
}

func TestApplyHints(t *testing.T) {
	h, err := LoadHints(writeHints(t, hintsYAML))
	if err != nil {
		t.Fatal(err)
	}
	w := hinted()
	before := shapeOf(w)
	if err := Apply(&w, h); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(shapeOf(w), before) {
		t.Fatalf("hints changed the elements, relations or views:\n%+v\n%+v", shapeOf(w), before)
	}
	if w.Elements[0].Title != "Mirror" || w.Views[0].Title != "Everything" || w.Views[0].Layout != "LeftRight" || w.Views[1].Layout != "TopBottom" {
		t.Fatalf("hints not applied: %+v %+v", w.Elements[0], w.Views)
	}
	if want := []Group{{Title: "Control plane", Include: []string{"mirror", "queue"}}}; !reflect.DeepEqual(w.Views[0].Groups, want) || w.Views[1].Groups != nil {
		t.Fatalf("groups %+v", w.Views)
	}
	files, err := w.Files()
	if err != nil {
		t.Fatal(err)
	}
	overview := string(files[filepath.Join("views", "overview.c4")])
	for _, want := range []string{"title 'Everything'", "    include configmap\n", "    group 'Control plane' {\n      include mirror, queue\n    }\n", "autoLayout LeftRight"} {
		if !strings.Contains(overview, want) {
			t.Errorf("overview view lacks %q:\n%s", want, overview)
		}
	}
	if !strings.Contains(string(files[filepath.Join("views", "flow.c4")]), "autoLayout TopBottom") {
		t.Errorf("flow view lacks its layout")
	}
	// The model file differs only in the hinted title.
	plain, err := hinted().Files()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Replace(string(files["model.c4"]), "'Mirror'", "'Mirror controller'", 1); got != string(plain["model.c4"]) {
		t.Fatalf("model.c4 changed beyond the title:\n%s", files["model.c4"])
	}
	likec4Validate(t, w)
}

func TestHintsRejectUnknownIDs(t *testing.T) {
	for name, tc := range map[string]struct{ edit, want string }{
		"unknown title":         {"    mirror: Mirror\n", "    mirrors: Mirror\n"},
		"unknown group element": {"      - queue\n", "      - queues\n"},
		"unknown layout view":   {"    flow: TopBottom\n", "    flows: TopBottom\n"},
		"unknown layout value":  {"    flow: TopBottom\n", "    flow: Diagonal\n"},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeHints(t, strings.Replace(hintsYAML, tc.edit, tc.want, 1))
			h, err := LoadHints(path)
			if err != nil {
				t.Fatal(err)
			}
			line := 0
			for i, l := range strings.Split(strings.Replace(hintsYAML, tc.edit, tc.want, 1), "\n") {
				if l+"\n" == tc.want {
					line = i + 1
				}
			}
			w := hinted()
			before := w.Views[0].Title
			err = Apply(&w, h)
			if err == nil || !strings.HasPrefix(err.Error(), path+":"+strconv.Itoa(line)+": spec.") {
				t.Fatalf("got %v, want a diagnostic at %s:%d", err, path, line)
			}
			if w.Views[0].Title != before || w.Elements[0].Title != "Mirror controller" {
				t.Fatal("a failed Apply changed the workspace")
			}
		})
	}
}

func TestHintsRejectBadDocuments(t *testing.T) {
	for name, content := range map[string]string{
		"unknown field": strings.Replace(hintsYAML, "  layout:\n", "  colours:\n    a: b\n  layout:\n", 1),
		"wrong kind":    strings.Replace(hintsYAML, "kind: ViewHints", "kind: ViewHint", 1),
	} {
		if _, err := LoadHints(writeHints(t, content)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	w := hinted()
	for name, h := range map[string]Hints{
		"element in two groups": {Groups: map[string][]string{"a": {"mirror"}, "b": {"mirror"}}},
		"empty group":           {Groups: map[string][]string{"a": nil}},
		"empty title":           {Titles: map[string]string{"mirror": ""}},
	} {
		if err := h.Validate(w); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	bad := hinted()
	bad.Views[0].Groups = []Group{{Title: "g", Include: []string{"missing"}}}
	if err := bad.Validate(); err == nil {
		t.Error("group of an element outside the view accepted")
	}
	bad = hinted()
	bad.Views[1].Layout = "Sideways"
	if err := bad.Validate(); err == nil {
		t.Error("unknown layout accepted")
	}
}

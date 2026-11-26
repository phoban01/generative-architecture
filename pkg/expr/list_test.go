package expr

import (
	"strings"
	"testing"
)

// A list of strings lowers to Quint's List: literals, size, indexing,
// concatenation, membership and the quantifiers over its elements.
func TestLowerListOfString(t *testing.T) {
	scope := map[string]Symbol{"items": {Type: ListOfString, Quint: "items"}, "job": {Type: "string", Quint: "job"}, "i": {Type: "int", Quint: "i"}}
	for _, tc := range []struct{ source, want, quint string }{
		{`[]`, ListOfString, `List()`},
		{`["a", "b"]`, ListOfString, `List("a", "b")`},
		{`size(items)`, "int", `length(items)`},
		{`items[i]`, "string", `items[i]`},
		{`items + [job]`, ListOfString, `items.concat(List(job))`},
		{`job in items`, "bool", `items.indices().exists(_i => (items[_i] == job))`},
		{`items.exists(x, x == "a")`, "bool", `items.indices().exists(x_i => (items[x_i] == "a"))`},
		{`items == []`, "bool", `(items == List())`},
	} {
		got, err := (Compiler{}).Compile(Expression(tc.source), scope, tc.want)
		if err != nil {
			t.Fatalf("%s: %v", tc.source, err)
		}
		if got.Quint != tc.quint {
			t.Errorf("%s lowered to %s, want %s", tc.source, got.Quint, tc.quint)
		}
	}
	for source, reason := range map[string]string{`[1]`: "list elements must be strings", `items["a"]`: "list index must be int", `1 in items`: "needs a string"} {
		if _, err := (Compiler{}).Compile(Expression(source), scope, ""); err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%s: got %v, want %q", source, err, reason)
		}
	}
	if qt, err := (Compiler{}).QuintType(ListOfString); err != nil || qt != "List[str]" {
		t.Fatalf("quint type %q %v", qt, err)
	}
}

package project

import (
	"strings"
	"testing"
)

func modelSpec() Spec {
	return Spec{
		Module:      "example.org/sample",
		Packages:    []string{"./controllers"},
		Abstraction: "abstraction.yaml",
		Runtime:     Runtime{Profile: "controller-runtime", MaxTicks: 10},
		Controllers: []Controller{{
			Receiver: "Reconciler", Name: "sample", Workers: 1, RetryTicks: 2, Key: "primary",
			Effects:    []ClientEffect{{Call: "sigs.k8s.io/controller-runtime/pkg/client.Client.Get", Effect: "readPrimary"}},
			ObjectKeys: map[string]string{"example.org/sample/controllers.Reconciler.Target": "secondary"},
			Locals:     map[string]string{"primary.data.value": "snapshot"},
		}},
		Initial: map[string]string{"primary": `"a"`},
	}
}

func TestValidateModelDeclarations(t *testing.T) {
	if err := modelSpec().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		edit func(*Spec)
		want string
	}{
		"no abstraction":  {func(s *Spec) { s.Abstraction = "" }, "no abstraction document"},
		"escaping path":   {func(s *Spec) { s.Abstraction = "../a.yaml" }, "inside its directory"},
		"no clock bound":  {func(s *Spec) { s.Runtime.MaxTicks = 0 }, "maxTicks"},
		"bad name":        {func(s *Spec) { s.Controllers[0].Name = "my-controller" }, "identifiers"},
		"duplicate":       {func(s *Spec) { s.Controllers = append(s.Controllers, s.Controllers[0]) }, "twice"},
		"no workers":      {func(s *Spec) { s.Controllers[0].Workers = 0 }, "positive"},
		"no key":          {func(s *Spec) { s.Controllers[0].Key = "" }, "reconcile request"},
		"no effects":      {func(s *Spec) { s.Controllers[0].Effects = nil }, "at least one"},
		"function call":   {func(s *Spec) { s.Controllers[0].Effects[0].Call = "example.org/pkg.Get" }, "<Receiver>"},
		"bad effect":      {func(s *Spec) { s.Controllers[0].Effects[0].Effect = "read-primary" }, "identifier"},
		"bad consistency": {func(s *Spec) { s.Controllers[0].Effects[0].Consistency = "eventual" }, "cached or direct"},
		"bad object key":  {func(s *Spec) { s.Controllers[0].ObjectKeys = map[string]string{"Target": "secondary"} }, "<Type>.<Field>"},
		"bad local":       {func(s *Spec) { s.Controllers[0].Locals = map[string]string{"primary": "snapshot"} }, "<go local>"},
		"empty initial":   {func(s *Spec) { s.Initial["primary"] = " " }, "empty"},
		"duplicate callee": {func(s *Spec) {
			s.Controllers[0].Effects = append(s.Controllers[0].Effects, s.Controllers[0].Effects[0])
		}, "declared twice"},
		"negative max ticks": {func(s *Spec) { s.Runtime.MaxTicks = -1 }, "positive"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := modelSpec()
			tc.edit(&s)
			if err := s.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestSelects(t *testing.T) {
	s := Spec{Packages: []string{"./controllers", "./api/..."}}
	for file, want := range map[string]bool{
		"controllers/a.go":       true,
		"controllers/sub/a.go":   false,
		"api/v1/types.go":        true,
		"api/types.go":           true,
		"apis/types.go":          false,
		"main.go":                false,
		"other/controllers/a.go": false,
	} {
		if got := s.Selects(file); got != want {
			t.Errorf("Selects(%s) = %v, want %v", file, got, want)
		}
	}
	if !(Spec{Packages: []string{"./..."}}).Selects("main.go") {
		t.Error("./... selects the whole module")
	}
}

func TestClientEffectSymbol(t *testing.T) {
	pkg, recv, method, err := ClientEffect{Call: "sigs.k8s.io/controller-runtime/pkg/client.Reader.Get"}.Symbol()
	if err != nil || pkg != "sigs.k8s.io/controller-runtime/pkg/client" || recv != "Reader" || method != "Get" {
		t.Fatalf("got %q %q %q %v", pkg, recv, method, err)
	}
}

func TestLoadHashesAbstraction(t *testing.T) {
	p, err := Load("../../cmd/arch/testdata/configmirror/project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if p.Hashes["abstraction.yaml"] == "" {
		t.Fatalf("abstraction not hashed: %v", p.Hashes)
	}
	docs := p.Documents()
	if docs[len(docs)-1] != "abstraction.yaml" {
		t.Fatalf("documents %v", docs)
	}
	if c, ok := p.Spec.Controller("Reconciler"); !ok || c.Name != "mirror" || len(c.Effects) != 3 {
		t.Fatalf("controller declaration %+v", c)
	}
}

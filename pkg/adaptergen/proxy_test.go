package adaptergen_test

import (
	"go/types"
	"strings"
	"testing"

	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/adaptergen/internal/fixture"
	"generative-architecture/pkg/ir"
)

func TestGenerateInterfaceProxy(t *testing.T) {
	iface, err := fixture.ProvisionerType()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateInterfaceProxy(fixture.Package, iface, fixture.ProvisionerEffects())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "proxy", src)
	p := typecheck(t, src)
	assertNoModelState(t, p)
	proxy, ok := p.Types.Scope().Lookup("ProvisionerProxy").(*types.TypeName)
	if !ok {
		t.Fatal("generated source defines no ProvisionerProxy")
	}
	// The generated package sees its own copy of the interface type.
	embedded := proxy.Type().Underlying().(*types.Struct).Field(0).Type()
	if !types.Implements(proxy.Type(), embedded.Underlying().(*types.Interface)) {
		t.Fatal("ProvisionerProxy does not implement Provisioner")
	}
	// Only the gated methods are generated; Status is promoted from the
	// embedded implementation.
	ms := types.NewMethodSet(proxy.Type())
	for _, m := range []string{"Provision", "Deprovision", "Tag"} {
		if sel := ms.Lookup(nil, m); sel == nil || len(sel.Index()) != 1 {
			t.Errorf("%s is not a method of the proxy itself", m)
		}
	}
	if sel := ms.Lookup(nil, "Status"); sel == nil || len(sel.Index()) != 2 {
		t.Errorf("Status is not promoted from the implementation")
	}
	text := string(src)
	for _, want := range []string{
		`result := p.Gate.Hook(a0, effect.Call{Descriptor: proxyProvisionerEffects["provision"], Submitted: map[string]any{"name": a1, "size": a2}}`,
		`p.Gate.Hook(p.Context, effect.Call{Descriptor: proxyProvisionerEffects["tag"], Submitted: map[string]any{"name": a0, "tags": a1}}`,
		`p.Provisioner.Tag(a0, a1...)`,
		`{"ready": false, "quotaExceeded": true},`,
		`Operation: "external"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated proxy lacks %q", want)
		}
	}
}

func TestGenerateInterfaceProxyRejects(t *testing.T) {
	iface, err := fixture.ProvisionerType()
	if err != nil {
		t.Fatal(err)
	}
	valid := fixture.ProvisionerEffects()
	noOutcomes := valid["Provision"]
	noOutcomes.Outcomes = nil
	sameID := valid["Deprovision"]
	sameID.ID = "provision"
	for name, tc := range map[string]struct {
		effects map[string]ir.Effect
		want    string
	}{
		"no effects":     {nil, "at least one effect"},
		"unknown method": {map[string]ir.Effect{"Resize": valid["Provision"]}, "has no method Resize"},
		"no outcomes":    {map[string]ir.Effect{"Provision": noOutcomes}, "at least one outcome"},
		"duplicate id":   {map[string]ir.Effect{"Provision": valid["Provision"], "Deprovision": sameID}, "distinct effect id"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := adaptergen.GenerateInterfaceProxy("p", iface, tc.effects)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got %v", tc.want, err)
			}
		})
	}
	if _, err := adaptergen.GenerateInterfaceProxy("p", types.NewNamed(types.NewTypeName(0, nil, "X", nil), types.Typ[types.Int], nil), valid); err == nil {
		t.Fatal("a type without a package was accepted")
	}
}

// Generated into the interface's own package, the proxy does not import it.
func TestGenerateInterfaceProxyInOwnPackage(t *testing.T) {
	iface, err := fixture.ProvisionerType()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateInterfaceProxy("fixture", iface, fixture.ProvisionerEffects())
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if strings.Contains(text, fixture.ProvisionerPath) || !strings.Contains(text, "\tProvisioner\n") {
		t.Fatalf("own-package proxy qualifies its interface:\n%s", text)
	}
}

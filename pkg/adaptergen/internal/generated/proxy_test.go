package generated

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"generative-architecture/pkg/adaptergen"
	"generative-architecture/pkg/adaptergen/internal/fixture"
	"generative-architecture/pkg/effect"
)

var _ fixture.Provisioner = ProvisionerProxy{}

func TestProxyUpToDate(t *testing.T) {
	iface, err := fixture.ProvisionerType()
	if err != nil {
		t.Fatal(err)
	}
	src, err := adaptergen.GenerateInterfaceProxy(fixture.Package, iface, fixture.ProvisionerEffects())
	if err != nil {
		t.Fatal(err)
	}
	assertGenerated(t, "proxy.go", src)
}

// provider is a stand-in implementation that records its calls.
type provider struct{ calls []string }

func (p *provider) Provision(_ context.Context, name string, size int) (string, error) {
	p.calls = append(p.calls, "provision "+name)
	return name + ".internal", nil
}
func (p *provider) Deprovision(name string) error {
	p.calls = append(p.calls, "deprovision "+name)
	return errors.New("provider is down")
}
func (p *provider) Status(name string) string { return "status of " + name }
func (p *provider) Tag(name string, tags ...string) {
	p.calls = append(p.calls, "tag "+name+" "+strings.Join(tags, ","))
}

type provisionResult struct {
	endpoint string
	err      error
}

// provision calls Provision through the proxy in the background, as the
// reconciler would.
func provision(ctx context.Context, proxy ProvisionerProxy) <-chan provisionResult {
	out := make(chan provisionResult, 1)
	go func() {
		endpoint, err := proxy.Provision(ctx, "db-1", 2)
		out <- provisionResult{endpoint, err}
	}()
	return out
}

func TestProxyPausesExternalEffects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	impl := &provider{}
	proxy := ProvisionerProxy{Provisioner: impl, Gate: effect.NewGate(), Context: ctx}

	// A declared success outcome calls the implementation and delivers
	// its result to the caller.
	done := provision(ctx, proxy)
	call, result, err := ReleaseProvisioner(ctx, proxy.Gate, "provision", "ready")
	if err != nil {
		t.Fatal(err)
	}
	if call.Descriptor.Operation != "external" || !reflect.DeepEqual(call.Submitted, map[string]any{"name": "db-1", "size": 2}) {
		t.Fatalf("gate saw %+v", call)
	}
	if got := <-done; got.endpoint != "db-1.internal" || got.err != nil || result.Snapshot["result0"] != "db-1.internal" {
		t.Fatalf("ready: caller got %+v, gate %+v", got, result)
	}

	// A declared error outcome reaches the caller without calling the
	// implementation.
	done = provision(ctx, proxy)
	if _, _, err := ReleaseProvisioner(ctx, proxy.Gate, "provision", "quotaExceeded"); err != nil {
		t.Fatal(err)
	}
	got := <-done
	var outcome *ProvisionerOutcomeError
	if !errors.As(got.err, &outcome) || outcome.Outcome != "quotaExceeded" || got.endpoint != "" {
		t.Fatalf("quotaExceeded: caller got %+v", got)
	}

	// An undeclared outcome is refused and the call stays paused until a
	// declared one releases it.
	done = provision(ctx, proxy)
	if _, _, err := ReleaseProvisioner(ctx, proxy.Gate, "provision", "exploded"); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared outcome: %v", err)
	}
	select {
	case got := <-done:
		t.Fatalf("call returned before release: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
	if _, _, err := ReleaseProvisioner(ctx, proxy.Gate, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	<-done

	// A success outcome whose call fails is reported to the harness.
	errs := make(chan error, 1)
	go func() { errs <- proxy.Deprovision("db-1") }()
	if _, _, err := ReleaseProvisioner(ctx, proxy.Gate, "deprovision", "released"); err == nil || !strings.Contains(err.Error(), "provider is down") {
		t.Fatalf("failed success outcome: %v", err)
	}
	<-errs

	// Ungated methods delegate directly.
	if proxy.Status("db-1") != "status of db-1" {
		t.Fatal("Status did not delegate")
	}
	want := []string{"provision db-1", "provision db-1", "deprovision db-1"}
	if !reflect.DeepEqual(impl.calls, want) {
		t.Fatalf("implementation calls %v, want %v", impl.calls, want)
	}
}

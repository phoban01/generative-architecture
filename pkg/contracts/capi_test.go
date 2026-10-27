package contracts

import (
	"github.com/phoban01/generative-architecture/pkg/krm"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const capiLibrary = "library/clusterapi"

// capiContracts lists every Cluster API library document and its reference.
var capiContracts = map[string]string{
	"infrastructure-machine-v1beta1.yaml": "capi.infrastructure-machine/v1beta1",
	"infrastructure-machine-v1beta2.yaml": "capi.infrastructure-machine/v1beta2",
	"infrastructure-cluster-v1beta1.yaml": "capi.infrastructure-cluster/v1beta1",
	"bootstrap-config-v1beta1.yaml":       "capi.bootstrap-config/v1beta1",
}

func TestClusterAPILibraryLoads(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(capiLibrary, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(capiContracts) {
		t.Fatalf("library has %d documents, test lists %d", len(files), len(capiContracts))
	}
	for name, ref := range capiContracts {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(capiLibrary, name)
			d, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := d.Name + "/" + d.Version; got != ref {
				t.Fatalf("got contract %s, want %s", got, ref)
			}
			// Every document pins the upstream release and cites its contract page.
			var spec Definition
			meta, err := krm.Read(path, "InteractionContract", &spec)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"architecture.local/upstream", "architecture.local/upstream-version", "architecture.local/reference"} {
				if meta.Annotations[key] == "" {
					t.Fatalf("missing annotation %s", key)
				}
			}
			if !strings.HasPrefix(meta.Annotations["architecture.local/reference"], "https://") {
				t.Fatalf("reference %q is not a URL", meta.Annotations["architecture.local/reference"])
			}
		})
	}
}

// capiProviderRoot writes a consumer module that requires the provider
// fixture as a dependency module, so selectors resolve it like an upstream
// provider in the module cache.
func capiProviderRoot(t *testing.T) string {
	t.Helper()
	fixture, err := filepath.Abs("testdata/capiprovider")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mod := "module example.org/consumer\n\ngo 1.25\n\nrequire example.org/capiprovider v0.0.0\n\nreplace example.org/capiprovider => " + fixture + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func loadCAPIContracts(t *testing.T) []Definition {
	t.Helper()
	defs := []Definition{}
	for name := range capiContracts {
		d, err := Load(filepath.Join(capiLibrary, name))
		if err != nil {
			t.Fatal(err)
		}
		defs = append(defs, d)
	}
	return defs
}

func TestClusterAPIContractsAgainstProviderFixture(t *testing.T) {
	root := capiProviderRoot(t)
	defs := loadCAPIContracts(t)
	for _, tc := range []struct {
		name       string
		contract   string
		goType     string
		wantPaths  map[string]string
		wantAbsent []string
		wantErr    string
	}{
		{
			name: "machine v1beta1 with optional fields", contract: "capi.infrastructure-machine/v1beta1", goType: "InfraMachine",
			wantPaths: map[string]string{"spec.providerID": "spec.providerID", "status.ready": "status.ready", "status.failureReason": "status.failureReason", "status.failureMessage": "status.failureMessage", "spec.failureDomain": "spec.failureDomain"},
		},
		{
			name: "machine v1beta1 required fields only", contract: "capi.infrastructure-machine/v1beta1", goType: "MinimalInfraMachine",
			wantPaths:  map[string]string{"spec.providerID": "spec.providerID", "status.ready": "status.ready"},
			wantAbsent: []string{"spec.failureDomain", "status.failureMessage", "status.failureReason"},
		},
		{
			name: "machine v1beta2", contract: "capi.infrastructure-machine/v1beta2", goType: "InfraMachineV1Beta2",
			wantPaths: map[string]string{"spec.providerID": "spec.providerID", "status.initialization.provisioned": "status.initialization.provisioned", "status.failureDomain": "status.failureDomain"},
		},
		{name: "v1beta1 machine against v1beta2", contract: "capi.infrastructure-machine/v1beta2", goType: "InfraMachine", wantErr: "field status.initialization.provisioned: missing contract field"},
		{name: "missing providerID", contract: "capi.infrastructure-machine/v1beta1", goType: "MachineWithoutProviderID", wantErr: "field spec.providerID: missing contract field spec.providerID"},
		{name: "ready is a string", contract: "capi.infrastructure-machine/v1beta1", goType: "MachineWithStringReady", wantErr: "field status.ready: expected bool, got string"},
		{name: "optional field with wrong type", contract: "capi.infrastructure-machine/v1beta1", goType: "MachineWithBoolFailureReason", wantErr: "field status.failureReason: expected *string, got bool"},
		{
			name: "cluster with endpoint", contract: "capi.infrastructure-cluster/v1beta1", goType: "InfraCluster",
			wantPaths: map[string]string{"status.ready": "status.ready", "spec.controlPlaneEndpoint.host": "spec.controlPlaneEndpoint.host", "spec.controlPlaneEndpoint.port": "spec.controlPlaneEndpoint.port", "status.failureReason": "status.failureReason", "status.failureMessage": "status.failureMessage"},
		},
		{
			name: "cluster with external endpoint", contract: "capi.infrastructure-cluster/v1beta1", goType: "ExternalEndpointCluster",
			wantPaths:  map[string]string{"status.ready": "status.ready"},
			wantAbsent: []string{"spec.controlPlaneEndpoint.host", "spec.controlPlaneEndpoint.port", "status.failureMessage", "status.failureReason"},
		},
		{name: "cluster port is a Go int", contract: "capi.infrastructure-cluster/v1beta1", goType: "ClusterWithIntPort", wantErr: "field spec.controlPlaneEndpoint.port: expected int32, got int"},
		{name: "cluster without ready", contract: "capi.infrastructure-cluster/v1beta1", goType: "ClusterWithoutReady", wantErr: "field status.ready: missing contract field status.ready"},
		{
			name: "bootstrap config", contract: "capi.bootstrap-config/v1beta1", goType: "BootstrapConfig",
			wantPaths: map[string]string{"status.ready": "status.ready", "status.dataSecretName": "status.dataSecretName", "status.failureReason": "status.failureReason", "status.failureMessage": "status.failureMessage"},
		},
		{name: "bootstrap without data secret", contract: "capi.bootstrap-config/v1beta1", goType: "BootstrapWithoutDataSecretName", wantErr: "field status.dataSecretName: missing contract field status.dataSecretName"},
		{name: "machine is not a bootstrap config", contract: "capi.bootstrap-config/v1beta1", goType: "InfraMachine", wantErr: "field status.dataSecretName: missing contract field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			impl := Implementation{Contract: tc.contract, Package: "example.org/capiprovider", Type: tc.goType}
			evidence, err := Inspect(root, defs, nil, []Implementation{impl})
			if tc.wantErr != "" {
				want := "external selector example.org/capiprovider." + tc.goType + " for " + tc.contract + ": " + tc.wantErr
				if err == nil || !strings.HasPrefix(err.Error(), want) {
					t.Fatalf("got error %v, want prefix %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			e := evidence[0]
			if e.Origin != "external-selector" || e.Module != "example.org/capiprovider" || e.Version != "v0.0.0" {
				t.Fatalf("unexpected provenance %s %s@%s", e.Origin, e.Module, e.Version)
			}
			if len(e.JSONPaths) != len(tc.wantPaths) {
				t.Fatalf("got JSON paths %v, want %v", e.JSONPaths, tc.wantPaths)
			}
			for logical, path := range tc.wantPaths {
				if e.JSONPaths[logical] != path {
					t.Fatalf("got JSON paths %v, want %v", e.JSONPaths, tc.wantPaths)
				}
			}
			if !slices.Equal(e.Absent, tc.wantAbsent) {
				t.Fatalf("got absent %v, want %v", e.Absent, tc.wantAbsent)
			}
			if e.SourceHashes["example.org/capiprovider@v0.0.0/machine.go"] == "" {
				t.Fatalf("missing fixture source hash in %v", e.SourceHashes)
			}
		})
	}
}

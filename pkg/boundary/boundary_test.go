package boundary_test

import (
	"github.com/phoban01/generative-architecture/pkg/boundary"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publisher is a boundary for a neutral upstream project that watches Source
// objects and writes Output objects.
const publisher = `apiVersion: architecture.local/v1alpha1
kind: BoundaryModel
metadata:
  name: publisher
spec:
  upstream: example.org/publisher
  version: v1.2.3
  digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  model: model.yaml
  owns:
  - {group: example.org, version: v1, kind: Source}
  watches:
  - id: sources
    resource: {group: example.org, version: v1, kind: Source}
  effects:
  - id: writeOutput
    resource: {group: example.org, version: v1, kind: Output}
    operation: apply
    consistency: direct
    options: {fieldManager: publisher}
  ports:
    output:
      state: published
      resource: {group: example.org, version: v1, kind: Output}
      direction: out
  assumptions:
  - id: periodic
    statement: The publisher reconciles every Source at a fixed interval.
    evidence: declared
    reference: https://example.org/publisher/docs/interval
    conformance: {kind: documentation, reference: https://example.org/publisher/docs/interval}
  - id: noDrift
    statement: The publisher never edits fields it does not own.
    evidence: assumed
    conformance: {kind: none}
`

const transitionModel = `apiVersion: architecture.local/v1alpha1
kind: TransitionModel
metadata:
  name: publisher
spec:
  name: Publisher
  state:
    published: {type: bool, initial: 'false'}
  actions:
  - name: publish
    guard: '!published'
    updates: {published: 'true'}
  properties: []
`

func TestLoad(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, err string
	}{
		{name: "valid"},
		{name: "assumption claims extracted", from: "evidence: declared", to: "evidence: extracted", err: "spec.assumptions[0].evidence: a boundary is not extracted or observed"},
		{name: "assumption claims observed", from: "evidence: assumed", to: "evidence: observed", err: "spec.assumptions[1].evidence: a boundary is not extracted or observed"},
		{name: "assumption is unresolved", from: "evidence: assumed", to: "evidence: unresolved", err: "spec.assumptions[1].evidence: a boundary is not extracted or observed"},
		{name: "unknown evidence kind", from: "evidence: declared", to: "evidence: guessed", err: `spec.assumptions[0].evidence: unknown evidence kind "guessed"`},
		{name: "unknown field", from: "    evidence: declared\n", to: "    evidence: declared\n    confidence: high\n", err: "field confidence not found"},
		{name: "missing upstream", from: "upstream: example.org/publisher", to: "upstream: ''", err: "spec.upstream: required"},
		{name: "missing version", from: "version: v1.2.3", to: "version: ''", err: "spec.version: required"},
		{name: "unpinned version", from: "version: v1.2.3", to: "version: latest", err: `spec.version: "latest" is not a semantic version`},
		{name: "bad digest", from: "digest: sha256:0123", to: "digest: md5:0123", err: "spec.digest:"},
		{name: "unknown conformance kind", from: "{kind: none}", to: "{kind: fuzz}", err: `spec.assumptions[1].conformance.kind: unknown kind "fuzz"`},
		{name: "documentation hook without reference", from: "{kind: documentation, reference: https://example.org/publisher/docs/interval}", to: "{kind: documentation}", err: "spec.assumptions[0].conformance.reference: required"},
		{name: "duplicate assumption", from: "id: noDrift", to: "id: periodic", err: `spec.assumptions[1].id: "periodic"`},
		{name: "unknown effect operation", from: "operation: apply", to: "operation: upsert", err: `spec.effects[0]: effect writeOutput: unknown operation "upsert"`},
		{name: "out port without write effect", from: "operation: apply", to: "operation: get", err: "spec.ports.output: out port for example.org/v1/Output has no write effect"},
		{name: "out port backed only across clusters", from: "    options: {fieldManager: publisher}\n", to: "    options: {fieldManager: publisher}\n    cluster: remote\n", err: "spec.ports.output: out port for example.org/v1/Output has no write effect in the boundary's cluster"},
		{name: "port state not in model", from: "state: published", to: "state: missing", err: `spec.ports.output.state: "missing" is not a state variable of model Publisher`},
		{name: "resource port with contract", from: "      direction: out\n", to: "      direction: out\n      contract: data\n", err: "spec.ports.output: a resource port takes its contract from the resource"},
		{name: "model escapes the directory", from: "model: model.yaml", to: "model: ../model.yaml", err: "spec.model: path must be project-relative"},
		{name: "missing contract file", from: "  model: model.yaml\n", to: "  model: model.yaml\n  contracts: [missing.yaml]\n", err: "spec.contracts[0]:"},
		{name: "wrong kind", from: "kind: BoundaryModel", to: "kind: TransitionModel", err: "expected architecture.local/v1alpha1 BoundaryModel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "boundary.yaml")
			doc := publisher
			if tc.from != "" {
				if !strings.Contains(doc, tc.from) {
					t.Fatalf("fixture does not contain %q", tc.from)
				}
				doc = strings.Replace(doc, tc.from, tc.to, 1)
			}
			if err := os.WriteFile(path, []byte(doc), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "model.yaml"), []byte(transitionModel), 0600); err != nil {
				t.Fatal(err)
			}
			b, err := boundary.Load(path)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %v, want error containing %q", err, tc.err)
				}
				if !strings.HasPrefix(err.Error(), path) {
					t.Fatalf("error %q does not start with the file", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if b.Name != "publisher" || b.Model == nil || b.Model.Name != "Publisher" || len(b.Files) != 2 || b.SHA256 == "" {
				t.Fatalf("loaded %+v", b)
			}
			p := b.Provenance()
			if err := p.Validate(); err != nil || p.Module != "example.org/publisher" || p.ModuleVersion != "v1.2.3" {
				t.Fatalf("provenance %+v: %v", p, err)
			}
			if e := b.Spec.Effects[0].IR("publisher", p); e.Evidence != ir.Assumed || e.Validate() != nil {
				t.Fatalf("effect %+v", e)
			}
		})
	}
}

func TestSpecValidateNeedsAssumptionOrContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec boundary.Spec
		err  string
	}{
		{name: "assumption only", spec: boundary.Spec{Upstream: "example.org/x", Version: "v0.1.0", Assumptions: []boundary.Assumption{{ID: "a", Statement: "s", Evidence: ir.Assumed, Conformance: boundary.Conformance{Kind: boundary.ConformanceNone}}}}},
		{name: "contract only", spec: boundary.Spec{Upstream: "example.org/x", Version: "v0.1.0", Contracts: []string{"c.yaml"}}},
		{name: "neither", spec: boundary.Spec{Upstream: "example.org/x", Version: "v0.1.0"}, err: "spec: at least one assumption or contract is required"},
		{name: "every problem reported", spec: boundary.Spec{}, err: "spec.upstream: required\nspec.version: required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if tc.err == "" && err != nil {
				t.Fatal(err)
			}
			if tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
				t.Fatalf("got %v, want %q", err, tc.err)
			}
		})
	}
}

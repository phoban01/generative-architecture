package composition_test

import (
	"generative-architecture/pkg/composition"
	"generative-architecture/pkg/ir"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publisherBoundary is an upstream project known only by its boundary model.
// It writes Output objects with server-side apply.
const publisherBoundary = `apiVersion: architecture.local/v1alpha1
kind: BoundaryModel
metadata:
  name: publisher
spec:
  upstream: example.org/publisher
  version: v1.2.3
  model: publisher-model.yaml
  watches:
  - id: sources
    resource: {group: example.org, version: v1, kind: Source}
  effects:
  - id: applyOutput
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
    statement: The publisher applies every Source at a fixed interval.
    evidence: assumed
    conformance: {kind: none}
`

const publisherModel = `apiVersion: architecture.local/v1alpha1
kind: TransitionModel
metadata:
  name: publisher
spec:
  name: Publisher
  state:
    published: {type: bool, initial: 'false'}
  actions:
  - name: apply
    guard: '!published'
    updates: {published: 'true'}
  properties: []
`

// consumerModel is an owned controller that reads the Output objects.
const consumerModel = `apiVersion: architecture.local/v1alpha1
kind: TransitionModel
metadata:
  name: consumer
spec:
  name: Consumer
  state:
    outputs: {type: bool, initial: 'false'}
    seen: {type: bool, initial: 'false'}
  actions:
  - name: observe
    guard: outputs && !seen
    updates: {seen: 'true'}
  properties: []
`

const system = `apiVersion: architecture.local/v1alpha1
kind: SystemComposition
metadata:
  name: system
spec:
  name: WithBoundary
  autoLink: true
  components:
  - name: publisher
    boundary: boundary.yaml
  - name: consumer
    model: consumer.yaml
    ports:
      outputs: {state: outputs, contract: 'resource:example.org/v1/Output', version: v1, direction: in}
  links: []
  properties:
  - name: seenAfterPublish
    expression: '!consumer.seen || publisher.published'
`

func TestLoadBoundaryComponent(t *testing.T) {
	for _, tc := range []struct {
		name, file, from, to, err string
		more                      []string // further old, new pairs in file
	}{
		{name: "boundary composes with an owned controller"},
		{name: "model and boundary together", file: "system.yaml", from: "    boundary: boundary.yaml\n", to: "    boundary: boundary.yaml\n    model: consumer.yaml\n", err: "spec.components[0] (publisher): model and boundary are exclusive"},
		{name: "ports on a boundary component", file: "system.yaml", from: "    boundary: boundary.yaml\n", to: "    boundary: boundary.yaml\n    ports: {extra: {state: published, contract: x, version: v1, direction: out}}\n", err: "spec.components[0] (publisher): ports:"},
		{name: "boundary without a model", file: "boundary.yaml", from: "  model: publisher-model.yaml\n", to: "", err: "spec.ports: ports need a model"},
		{name: "boundary without a model or ports", file: "boundary.yaml", from: "  model: publisher-model.yaml\n", to: "", more: []string{"  ports:\n    output:\n      state: published\n      resource: {group: example.org, version: v1, kind: Output}\n      direction: out\n", ""}, err: "a boundary component needs spec.model"},
		{name: "boundary claims extracted evidence", file: "boundary.yaml", from: "evidence: assumed", to: "evidence: extracted", err: "spec.assumptions[0].evidence: a boundary is not extracted or observed"},
		{name: "boundary escapes the project", file: "system.yaml", from: "boundary: boundary.yaml", to: "boundary: ../boundary.yaml", err: "boundary: path must be project-relative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{"system.yaml": system, "boundary.yaml": publisherBoundary, "publisher-model.yaml": publisherModel, "consumer.yaml": consumerModel}
			if tc.file != "" {
				edits := append([]string{tc.from, tc.to}, tc.more...)
				for i := 0; i < len(edits); i += 2 {
					if !strings.Contains(files[tc.file], edits[i]) {
						t.Fatalf("%s does not contain %q", tc.file, edits[i])
					}
					files[tc.file] = strings.Replace(files[tc.file], edits[i], edits[i+1], 1)
				}
			}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			loaded, err := composition.Load(root, "system.yaml")
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %v, want error containing %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"system.yaml", "boundary.yaml", "publisher-model.yaml", "consumer.yaml"} {
				if _, ok := loaded.Inputs[filepath.Join(root, name)]; !ok {
					t.Errorf("receipt misses %s: %v", name, loaded.Inputs)
				}
			}
			if len(loaded.Components) != 2 {
				t.Fatalf("components %+v", loaded.Components)
			}
			publisher, consumer := loaded.Components[0], loaded.Components[1]
			if consumer.Evidence != "" || len(consumer.Effects) != 0 {
				t.Errorf("owned component tagged: %+v", consumer)
			}
			if publisher.Evidence != ir.Assumed {
				t.Errorf("boundary component evidence %q, want assumed", publisher.Evidence)
			}
			if len(publisher.Effects) != 1 || len(publisher.Watches) != 1 {
				t.Fatalf("boundary facts %+v %+v", publisher.Effects, publisher.Watches)
			}
			e, w := publisher.Effects[0], publisher.Watches[0]
			if e.Evidence != ir.Assumed || e.Actor != "publisher" || e.Options["fieldManager"] != "publisher" || e.Provenance.ModuleVersion != "v1.2.3" || e.Validate() != nil {
				t.Errorf("boundary effect %+v", e)
			}
			if w.Evidence != ir.Assumed || w.Validate() != nil {
				t.Errorf("boundary watch %+v", w)
			}
			port := publisher.Ports["output"]
			if port.Evidence != ir.Assumed || port.Contract != "resource:example.org/v1/Output" || port.Version != composition.ResourceContractVersion {
				t.Errorf("boundary port %+v", port)
			}
			if got := loaded.Result.StateNames["consumer.outputs"]; got != "publisher_published" {
				t.Errorf("autoLink did not bind the boundary port: consumer.outputs -> %s", got)
			}
			links, diags, err := composition.JoinByResource(loaded.Components, nil, nil)
			if err != nil || len(diags) != 0 || len(links) != 1 || links[0].From != "publisher.output" {
				t.Errorf("join by resource: %v %v %v", links, diags, err)
			}
		})
	}
}

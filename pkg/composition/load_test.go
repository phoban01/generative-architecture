package composition_test

import (
	"github.com/phoban01/generative-architecture/pkg/composition"
	"github.com/phoban01/generative-architecture/pkg/ir"
	"os"
	"path/filepath"
	"reflect"
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
		{name: "ir and boundary together", file: "system.yaml", from: "    boundary: boundary.yaml\n", to: "    boundary: boundary.yaml\n    ir: ir.yaml\n", err: "spec.components[0] (publisher): ir and boundary are exclusive"},
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

// pairIR is an extracted IR of two controllers. The owner's primary watch
// is Thing and it creates Secrets; the user watches Things and reads
// Secrets.
const pairIR = `apiVersion: architecture.local/v1alpha1
kind: ArchitectureIR
metadata:
  name: pair
status:
  schema: v1alpha1
  controllers:
  - id: example.org/owner.OwnerReconciler
    name: OwnerReconciler
    provenance: {generator: test, source: {sha256: '00'}}
  - id: example.org/user.UserReconciler
    name: UserReconciler
    provenance: {generator: test, source: {sha256: '00'}}
  watches:
  - id: ownerThings
    controller: example.org/owner.OwnerReconciler
    resource: {group: example.org, version: v1, kind: Thing}
    primary: true
    mapping: {kind: self}
    evidence: extracted
    provenance: {generator: test, source: {sha256: '00'}}
  - id: userThings
    controller: example.org/user.UserReconciler
    resource: {group: example.org, version: v1, kind: Thing}
    mapping: {kind: self}
    evidence: extracted
    provenance: {generator: test, source: {sha256: '00'}}
  effects:
  - id: createSecret
    actor: example.org/owner.OwnerReconciler
    resource: {group: '', version: v1, kind: Secret}
    operation: create
    consistency: direct
    evidence: extracted
    provenance: {generator: test, source: {sha256: '00'}}
  - id: getSecret
    actor: example.org/user.UserReconciler
    resource: {group: '', version: v1, kind: Secret}
    operation: get
    consistency: cached
    evidence: declared
    provenance: {generator: test, source: {sha256: '00'}}
`

const ownerModel = `apiVersion: architecture.local/v1alpha1
kind: TransitionModel
metadata:
  name: owner
spec:
  name: Owner
  state:
    thing_objects: {type: int, initial: '0'}
    secret_objects: {type: int, initial: '0'}
  actions:
  - name: create
    guard: 'true'
    updates: {secret_objects: 'secret_objects + 1'}
  properties: []
`

const userModel = `apiVersion: architecture.local/v1alpha1
kind: TransitionModel
metadata:
  name: user
spec:
  name: User
  state:
    thing_objects: {type: int, initial: '0'}
    secret_objects: {type: int, initial: '0'}
    seen: {type: int, initial: '0'}
  actions:
  - name: observe
    guard: 'true'
    updates: {seen: 'thing_objects + secret_objects'}
  properties: []
`

// pairSystem composes the two controllers from their IR with no
// hand-written port list.
const pairSystem = `apiVersion: architecture.local/v1alpha1
kind: SystemComposition
metadata:
  name: pair
spec:
  name: Pair
  components:
  - name: owner
    model: owner.yaml
    ir: ir.yaml
    controller: OwnerReconciler
  - name: user
    model: user.yaml
    ir: ir.yaml
    controller: example.org/user.UserReconciler
  links: []
  properties: []
`

// A component that names an IR document derives its resource ports from
// the controller's watches and effects, and Load joins them by resource.
func TestLoadDerivesPortsFromIR(t *testing.T) {
	for _, tc := range []struct {
		name, file, from, to, err string
		diagnostics               int
		code                      string
	}{
		{name: "ports derive and join"},
		{name: "no creator is unresolved", file: "ir.yaml", from: "    operation: create\n", to: "    operation: update\n", err: "unresolved provider", diagnostics: 2, code: composition.DiagUnresolvedProvider},
		{name: "a declared port duplicates a derived one", file: "system.yaml", from: "    controller: example.org/user.UserReconciler\n", to: "    controller: example.org/user.UserReconciler\n    ports:\n      secrets: {state: seen, contract: 'resource:/v1/Secret', version: v1, direction: in}\n", err: "duplicate port", diagnostics: 1, code: composition.DiagDuplicatePort},
		{name: "clusters separate the join", file: "system.yaml", from: "    controller: example.org/user.UserReconciler\n", to: "    controller: example.org/user.UserReconciler\n    cluster: east\n", err: "unresolved provider", diagnostics: 2, code: composition.DiagUnresolvedProvider},
		{name: "two controllers need a selection", file: "system.yaml", from: "    controller: OwnerReconciler\n", to: "", err: "ir ir.yaml holds 2 controllers; set controller to one of example.org/owner.OwnerReconciler, example.org/user.UserReconciler"},
		{name: "unknown controller", file: "system.yaml", from: "controller: OwnerReconciler", to: "controller: Nobody", err: `holds no controller "Nobody"`},
		{name: "ir needs a model", file: "system.yaml", from: "    model: owner.yaml\n", to: "", err: "spec.components[0] (owner): model or boundary required"},
		{name: "ir escapes the project", file: "system.yaml", from: "    ir: ir.yaml\n    controller: OwnerReconciler\n", to: "    ir: ../ir.yaml\n    controller: OwnerReconciler\n", err: "ir: path must be project-relative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{"system.yaml": pairSystem, "ir.yaml": pairIR, "owner.yaml": ownerModel, "user.yaml": userModel}
			if tc.file != "" {
				if !strings.Contains(files[tc.file], tc.from) {
					t.Fatalf("%s does not contain %q", tc.file, tc.from)
				}
				files[tc.file] = strings.Replace(files[tc.file], tc.from, tc.to, 1)
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
				if len(loaded.Diagnostics) != tc.diagnostics || (tc.diagnostics > 0 && loaded.Diagnostics[0].Code != tc.code) {
					t.Fatalf("diagnostics %+v, want %d of %q", loaded.Diagnostics, tc.diagnostics, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := loaded.Inputs[filepath.Join(root, "ir.yaml")]; !ok {
				t.Errorf("receipt misses ir.yaml: %v", loaded.Inputs)
			}
			want := []composition.Link{{From: "owner.secret_v1", To: "user.secret_v1"}, {From: "owner.thing_v1_example_org", To: "user.thing_v1_example_org"}}
			if !reflect.DeepEqual(loaded.Links, want) {
				t.Fatalf("links %+v, want %+v", loaded.Links, want)
			}
			if len(loaded.Diagnostics) != 0 || len(loaded.Components) != 2 {
				t.Fatalf("diagnostics %+v, components %d", loaded.Diagnostics, len(loaded.Components))
			}
			owner, user := loaded.Components[0], loaded.Components[1]
			if owner.Evidence != ir.Extracted || len(owner.Watches) != 1 || len(owner.Effects) != 1 {
				t.Errorf("owner facts: evidence %q, %d watches, %d effects", owner.Evidence, len(owner.Watches), len(owner.Effects))
			}
			if user.Evidence != ir.Declared {
				t.Errorf("user evidence %q, want the weakest fact, declared", user.Evidence)
			}
			if p := owner.Ports["thing_v1_example_org"]; !p.Owner || p.Direction != "out" || p.State != "thing_objects" {
				t.Errorf("owner thing port %+v", p)
			}
			if p := owner.Ports["secret_v1"]; !p.Creator || p.Direction != "out" {
				t.Errorf("owner secret port %+v", p)
			}
			if p := user.Ports["secret_v1"]; p.Direction != "in" || p.Writable || p.Evidence != ir.Declared {
				t.Errorf("user secret port %+v", p)
			}
			for from, to := range map[string]string{"user.thing_objects": "owner_thing_objects", "user.secret_objects": "owner_secret_objects"} {
				if got := loaded.Result.StateNames[from]; got != to {
					t.Errorf("%s -> %s, want %s", from, got, to)
				}
			}
		})
	}
}

// A boundary whose out port carries an authored contract, because the
// kinds it applies depend on the manifests, takes the kinds from the
// system document: each becomes an assumed apply effect and a resource out
// port on the port's state, so the applied objects join by resource.
func TestLoadBoundaryApplies(t *testing.T) {
	authored := strings.Replace(publisherBoundary, "      resource: {group: example.org, version: v1, kind: Output}\n", "      contract: example.org/published\n      version: v1\n", 1)
	if authored == publisherBoundary {
		t.Fatal("the publisher boundary did not change")
	}
	applied := strings.Replace(system, "    boundary: boundary.yaml\n", "    boundary: boundary.yaml\n    applies:\n      output:\n        - {group: example.org, version: v1, kind: Output}\n", 1)
	for _, tc := range []struct {
		name, system, boundary, err string
	}{
		{name: "the applied kind joins by resource", system: applied, boundary: authored},
		{name: "a model component applies nothing", system: strings.Replace(system, "    model: consumer.yaml\n", "    model: consumer.yaml\n    applies: {outputs: [{group: example.org, version: v1, kind: Output}]}\n", 1), boundary: publisherBoundary, err: "(consumer): applies: only a boundary component applies kinds"},
		{name: "the port must exist", system: strings.Replace(applied, "      output:\n", "      results:\n", 1), boundary: authored, err: "applies.results: the boundary has no port results"},
		{name: "the port must carry an authored contract", system: applied, boundary: publisherBoundary, err: "applies.output: applies names the kinds behind an out port with an authored contract"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range map[string]string{"system.yaml": tc.system, "boundary.yaml": tc.boundary, "publisher-model.yaml": publisherModel, "consumer.yaml": consumerModel} {
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
			publisher := loaded.Components[0]
			port, ok := publisher.Ports["output_v1_example_org"]
			if !ok || port.State != "published" || port.Contract != "resource:example.org/v1/Output" || port.Direction != "out" || !port.Creator || port.Evidence != ir.Assumed {
				t.Fatalf("derived port %+v (%v)", port, ok)
			}
			if len(publisher.Effects) != 2 {
				t.Fatalf("effects %+v", publisher.Effects)
			}
			e := publisher.Effects[1]
			if e.ID != "outputOutput" || e.Operation != ir.OpApply || e.Evidence != ir.Assumed || e.Actor != "publisher" || e.Resource.Kind != "Output" {
				t.Fatalf("apply effect %+v", e)
			}
			if got := loaded.Result.StateNames["consumer.outputs"]; got != "publisher_published" {
				t.Errorf("autoLink did not bind the applied kind: consumer.outputs -> %s", got)
			}
			links, diags, err := composition.JoinByResource(loaded.Components, nil, nil)
			if err != nil || len(diags) != 0 || len(links) != 1 || links[0].From != "publisher.output_v1_example_org" || links[0].To != "consumer.outputs" {
				t.Errorf("join by resource: %v %v %v", links, diags, err)
			}
		})
	}
}

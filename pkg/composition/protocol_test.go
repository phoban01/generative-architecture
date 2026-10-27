package composition_test

import (
	"generative-architecture/pkg/composition"
	"generative-architecture/pkg/expr"
	"generative-architecture/pkg/krm"
	"generative-architecture/pkg/model"
	"generative-architecture/pkg/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// handshake says the provider marks the shared value ready before the
// consumer may consume it.
func handshake() protocol.Contract {
	return protocol.Contract{
		Name: "Handshake", Version: "v1",
		Roles:   protocol.Roles{Provider: "producer", Consumer: "user"},
		States:  []string{"waiting", "ready", "done"},
		Initial: "waiting",
		Transitions: []protocol.Transition{
			{Name: "prepare", From: "waiting", To: "ready", Actor: protocol.Provider, Guard: `next == "ready"`},
			{Name: "consume", From: "ready", To: "done", Actor: protocol.Consumer, Guard: `port == "ready" && next == "consumed"`},
		},
		Properties: []protocol.Property{{Name: "doneMeansConsumed", Expression: `state != "done" || port == "consumed"`}},
	}
}

func handshakeComponents() []composition.Component {
	producer := model.Spec{Name: "Producer", State: map[string]model.Variable{"value": {Type: "string", Initial: `""`}}, Actions: []model.Action{{Name: "prepare", Guard: "true", Updates: map[string]expr.Expression{"value": `"ready"`}}}}
	user := model.Spec{Name: "User", State: map[string]model.Variable{"input": {Type: "string", Initial: `""`}, "seen": {Type: "string", Initial: `""`}}, Actions: []model.Action{
		{Name: "consume", Guard: "true", Updates: map[string]expr.Expression{"input": `"consumed"`}},
		{Name: "look", Guard: "true", Updates: map[string]expr.Expression{"seen": "input"}},
	}}
	return []composition.Component{
		{Name: "producer", Model: producer, Ports: map[string]composition.Port{"output": {State: "value", Contract: "data", Version: "v1", Direction: "out"}}},
		{Name: "user", Model: user, Ports: map[string]composition.Port{"input": {State: "input", Contract: "data", Version: "v1", Direction: "in", Writable: true}}},
	}
}

var handshakeLink = composition.Link{From: "producer.output", To: "user.input", Protocol: "handshake"}

func TestProtocolPropertiesOverTrace(t *testing.T) {
	result, err := composition.ComposeWithProtocols("Joined", handshakeComponents(), []composition.Link{handshakeLink}, nil, map[string]protocol.Contract{"handshake": handshake()})
	if err != nil {
		t.Fatal(err)
	}
	const monitor = "producer_output__user_input_protoState"
	want := composition.ProtocolCheck{Link: handshakeLink, Protocol: "Handshake", Version: "v1", State: monitor, Properties: []string{"producer_output__user_input_conforms", "producer_output__user_input_doneMeansConsumed"}}
	if len(result.Protocols) != 1 || result.Protocols[0].State != want.State || strings.Join(result.Protocols[0].Properties, ",") != strings.Join(want.Properties, ",") {
		t.Fatalf("protocol checks %+v", result.Protocols)
	}
	m, err := model.Compile(result.Model)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		events   []string
		violated string
	}{
		{name: "in order", events: []string{"producer_prepare", "user_look", "user_consume"}},
		{name: "consumer writes before provider is ready", events: []string{"user_consume"}, violated: "producer_output__user_input_conforms"},
		{name: "consumer writes twice", events: []string{"producer_prepare", "user_consume", "user_consume"}, violated: "producer_output__user_input_conforms"},
		{name: "reads are not protocol steps", events: []string{"user_look", "user_look"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, err := m.Initial()
			if err != nil {
				t.Fatal(err)
			}
			var failure error
			for _, event := range tc.events {
				if state, err = m.Next(state, event); err != nil {
					t.Fatal(err)
				}
				if failure = m.Check(state, event); failure != nil {
					break
				}
			}
			if tc.violated == "" && failure != nil {
				t.Fatalf("unexpected violation: %v", failure)
			}
			if tc.violated != "" && (failure == nil || !strings.Contains(failure.Error(), tc.violated)) {
				t.Fatalf("got %v, want %s violated", failure, tc.violated)
			}
		})
	}
	// The authored property holds independently of the monitor: a hand-built
	// state that claims done without the consumed value violates it.
	err = m.Check(map[string]any{monitor: "done", "producer_value": "ready", "user_seen": ""}, "user_look")
	if err == nil || !strings.Contains(err.Error(), "producer_output__user_input_doneMeansConsumed") {
		t.Fatalf("authored property not generated: %v", err)
	}
	quintTypecheck(t, m.Quint())
}

func quintTypecheck(t *testing.T, source string) {
	t.Helper()
	exe := os.Getenv("QUINT_BIN")
	if exe == "" {
		t.Log("QUINT_BIN unset; skipping Quint typecheck")
		return
	}
	path := filepath.Join(t.TempDir(), "Joined.qnt")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(exe, "typecheck", path).CombinedOutput(); err != nil {
		t.Fatalf("quint typecheck: %v\n%s", err, out)
	}
}

func TestProtocolLinkValidation(t *testing.T) {
	readOnly := func() []composition.Component {
		c := handshakeComponents()
		p := c[1].Ports["input"]
		p.Writable = false
		c[1].Ports["input"] = p
		c[1].Model.Actions = c[1].Model.Actions[1:]
		return c
	}
	unknownSymbol := handshake()
	unknownSymbol.Transitions[0].Guard = "other == 1"
	invalid := handshake()
	invalid.Initial = "nowhere"
	for _, tc := range []struct {
		name       string
		components []composition.Component
		protocols  map[string]protocol.Contract
		err        string
	}{
		{"contract not loaded", handshakeComponents(), nil, "link producer.output -> user.input: protocol handshake: contract not loaded"},
		{"consumer role on read-only port", readOnly(), map[string]protocol.Contract{"handshake": handshake()}, "roles do not match port directions: transitions[1] consume"},
		{"invalid contract", handshakeComponents(), map[string]protocol.Contract{"handshake": invalid}, "spec.initial"},
		{"guard names unknown symbol", handshakeComponents(), map[string]protocol.Contract{"handshake": unknownSymbol}, "unknown CEL symbol other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := composition.ComposeWithProtocols("Joined", tc.components, []composition.Link{handshakeLink}, nil, tc.protocols)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("error %v, want %q", err, tc.err)
			}
		})
	}
}

func TestAutoLinkAttachesNoProtocol(t *testing.T) {
	c := handshakeComponents()
	links, err := composition.AutoLink(c, nil)
	if err != nil || len(links) != 1 || links[0].Protocol != "" {
		t.Fatalf("%v %v", links, err)
	}
	result, err := composition.Compose("Joined", c, links, nil)
	if err != nil || len(result.Protocols) != 0 {
		t.Fatalf("auto-linked binding reported protocol checks: %v %v", result.Protocols, err)
	}
}

func TestLoadProtocolLink(t *testing.T) {
	root := t.TempDir()
	c := handshakeComponents()
	for i, file := range []string{"producer.yaml", "user.yaml"} {
		if err := krm.Write(filepath.Join(root, file), "TransitionModel", c[i].Name, c[i].Model); err != nil {
			t.Fatal(err)
		}
	}
	if err := krm.Write(filepath.Join(root, "handshake.yaml"), protocol.Kind, "handshake", handshake()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, protocol, err string
	}{
		{name: "loaded and hashed", protocol: "handshake.yaml"},
		{name: "missing file", protocol: "absent.yaml", err: "links[0].protocol"},
		{name: "escapes root", protocol: "../handshake.yaml", err: "links[0].protocol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := composition.Document{Name: "Joined", Components: []composition.ComponentRef{{Name: "producer", Model: "producer.yaml", Ports: c[0].Ports}, {Name: "user", Model: "user.yaml", Ports: c[1].Ports}}, Links: []composition.Link{{From: "producer.output", To: "user.input", Protocol: tc.protocol}}}
			if err := krm.Write(filepath.Join(root, "system.yaml"), "SystemComposition", "joined", doc); err != nil {
				t.Fatal(err)
			}
			loaded, err := composition.Load(root, "system.yaml")
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded.Result.Protocols) != 1 || len(loaded.Inputs) != 4 {
				t.Fatalf("protocols %v inputs %v", loaded.Result.Protocols, loaded.Inputs)
			}
		})
	}
}

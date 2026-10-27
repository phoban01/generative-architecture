package protocol_test

import (
	"github.com/phoban01/generative-architecture/pkg/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requestResponse is a two-state protocol: the consumer requests, then the
// provider responds.
const requestResponse = `apiVersion: architecture.local/v1alpha1
kind: ProtocolContract
metadata:
  name: request-response
spec:
  name: RequestResponse
  version: v1
  roles:
    provider: server
    consumer: client
  states: [idle, requested]
  initial: idle
  transitions:
  - name: request
    from: idle
    to: requested
    actor: consumer
    guard: next == "request"
  - name: respond
    from: requested
    to: idle
    actor: provider
    guard: next == "response"
  properties:
  - name: answered
    expression: state == "idle" || port == "request"
`

func TestLoad(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, err string
	}{
		{name: "valid request/response"},
		{name: "unknown state reference", from: "    to: idle\n", to: "    to: done\n", err: `spec.transitions[1].to: unknown state "done"`},
		{name: "unknown initial", from: "initial: idle", to: "initial: busy", err: `spec.initial: unknown state "busy"`},
		{name: "unknown role", from: "actor: provider", to: "actor: observer", err: `spec.transitions[1].actor: unknown role "observer"`},
		{name: "duplicate state", from: "[idle, requested]", to: "[idle, idle]", err: `spec.states[1]: duplicate state "idle"`},
		{name: "reserved state", from: "[idle, requested]", to: "[idle, requested, violation]", err: `spec.states[2]`},
		{name: "unknown field", from: "  version: v1\n", to: "  version: v1\n  owner: me\n", err: "field owner not found"},
		{name: "unknown role field", from: "    consumer: client\n", to: "    consumer: client\n    observer: x\n", err: "field observer not found"},
		{name: "missing version", from: "  version: v1\n", to: "", err: "spec.version: required"},
		{name: "same role names", from: "consumer: client", to: "consumer: server", err: "spec.roles"},
		{name: "guard is not CEL", from: `guard: next == "response"`, to: `guard: next ==`, err: "spec.transitions[1].guard"},
		{name: "property is not CEL", from: `expression: state == "idle" || port == "request"`, to: `expression: "state =="`, err: "spec.properties[0].expression"},
		{name: "duplicate transition", from: "name: respond", to: "name: request", err: "spec.transitions[1].name"},
		{name: "wrong kind", from: "kind: ProtocolContract", to: "kind: Contract", err: "expected architecture.local/v1alpha1 ProtocolContract"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(requestResponse, tc.from, tc.to, 1)
			if tc.from != "" && body == requestResponse {
				t.Fatalf("mutation %q did not apply", tc.from)
			}
			path := filepath.Join(t.TempDir(), "protocol.yaml")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := protocol.Load(path)
			if tc.err == "" {
				if err != nil {
					t.Fatal(err)
				}
				if c.Name != "RequestResponse" || len(c.Transitions) != 2 || c.Transitions[0].Actor != protocol.Consumer {
					t.Fatalf("loaded %+v", c)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.err) || !strings.Contains(err.Error(), path) {
				t.Fatalf("error %v, want located %q", err, tc.err)
			}
		})
	}
}

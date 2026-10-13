package runtimecheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"generative-architecture/examples/machinevendor/architecture"
	"os"
)

func ReadTrace(path string) (architecture.Trace, error) { return readRuntimeTrace(path, false) }
func ReadCompositionTrace(path string) (architecture.Trace, error) {
	return readRuntimeTrace(path, true)
}
func readRuntimeTrace(path string, allowViolation bool) (architecture.Trace, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return architecture.Trace{}, err
	}
	var raw struct {
		Meta struct {
			Status string `json:"status"`
		} `json:"#meta"`
		States []map[string]any `json:"states"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&raw); err != nil {
		return architecture.Trace{}, err
	}
	out := architecture.Trace{Status: raw.Meta.Status}
	if (raw.Meta.Status != "ok" && !(allowViolation && raw.Meta.Status == "violation")) || len(raw.States) < 2 {
		return out, fmt.Errorf("expected successful runtime trace")
	}
	for i, s := range raw.States {
		v, err := architecture.DecodeITF(s)
		if err != nil {
			return out, err
		}
		state := v.(map[string]any)
		if _, ok := state["event"].(string); !ok {
			return out, fmt.Errorf("missing runtime event")
		}
		if i == 0 && state["event"] != "init" {
			return out, fmt.Errorf("trace must start at init")
		}
		if state["tick"] != int64(i) {
			return out, fmt.Errorf("nonsequential trace")
		}
		if _, ok := state["s"].(map[string]any); !ok {
			return out, fmt.Errorf("missing state")
		}
		out.States = append(out.States, state)
	}
	return out, nil
}

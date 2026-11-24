package quint

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Quint writes its run metadata into the #meta block of every ITF trace:
// a description with the date, the path of the module under a temporary
// directory and a timestamp. None of it describes the trace. The trace
// hashes in a report and the receipts cover the trace bytes, so an
// unchanged model would produce new hashes on every refresh. Sample strips
// the run metadata before it returns a trace.
var runMetadata = []string{"description", "source", "timestamp"}

// stripRunMetadata removes Quint's run metadata from the #meta block of
// the ITF trace b and returns the trace with the block rewritten. Every
// other member of the document keeps its bytes. A trace without a #meta
// block is returned unchanged.
func stripRunMetadata(b []byte) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("itf: %w", err)
	}
	raw, ok := doc["#meta"]
	if !ok {
		return b, nil
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("itf #meta: %w", err)
	}
	for _, k := range runMetadata {
		delete(meta, k)
	}
	rewritten, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	// Replace the block in place so the states keep their bytes and order.
	start := bytes.Index(b, raw)
	if start < 0 {
		return nil, fmt.Errorf("itf #meta: block not found in document")
	}
	out := make([]byte, 0, len(b)-len(raw)+len(rewritten))
	out = append(out, b[:start]...)
	out = append(out, rewritten...)
	out = append(out, b[start+len(raw):]...)
	return out, nil
}

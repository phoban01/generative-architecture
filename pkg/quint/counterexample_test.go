package quint

import (
	"strings"
	"testing"
)

// tlcViolation is the shape of quint verify output for a violated
// temporal property with the TLC backend.
const tlcViolation = `Error: Temporal property q_temporalProps was violated.

Error: The following behavior constitutes a counter-example:

State 1: <Initial predicate>
/\ count = 0
/\ event = "init"

State 2: <increment line 14, col 8 to line 14, col 29 of module M>
/\ count = 1
/\ event = "increment"

State 3: Stuttering
Finished checking temporal properties in 00s

[violation] Found an issue (580ms).
`

func TestCounterexample(t *testing.T) {
	got := counterexample(tlcViolation)
	want := `3 states; events: init -> increment -> stuttering; last state: count = 1, event = "increment"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	long := strings.Repeat("State 9: <a>\n/\\ event = \"step\"\n\n", 1000)
	if got := counterexample(long); len(got) > maxDetail+4 || !strings.HasSuffix(got, " ...") {
		t.Fatalf("summary not bounded: %d bytes", len(got))
	}
	if got := counterexample("no states\n"); got != "no states" {
		t.Fatal(got)
	}
}

func TestParseTLCTrace(t *testing.T) {
	got, err := parseTLCTrace(tlcViolation)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "violation" || len(got.States) != 2 {
		t.Fatalf("trace %+v", got)
	}
	if got.States[0].Event != "init" || got.States[0].Values["count"] != int64(0) || got.States[1].Event != "increment" || got.States[1].Values["count"] != int64(1) {
		t.Fatalf("states %+v", got.States)
	}
	if _, err := parseTLCTrace("State 1: <x>\n/\\ count = {1, 2}\n/\\ event = \"init\"\n"); err == nil {
		t.Fatal("a set value parsed")
	}
	if _, err := parseTLCTrace("no states\n"); err == nil {
		t.Fatal("empty output parsed")
	}
}

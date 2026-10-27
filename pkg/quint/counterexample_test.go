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

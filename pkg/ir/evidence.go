package ir

import "fmt"

// Evidence says how a fact is known. Facts extracted from source or observed
// at runtime outrank those declared by hand, assumed by a generator, or left
// unresolved.
type Evidence string

const (
	Extracted  Evidence = "extracted"
	Observed   Evidence = "observed"
	Declared   Evidence = "declared"
	Assumed    Evidence = "assumed"
	Unresolved Evidence = "unresolved"
)

var evidenceRank = map[Evidence]int{Extracted: 5, Observed: 4, Declared: 3, Assumed: 2, Unresolved: 1}

// Validate rejects values outside the five evidence kinds, including empty.
func (e Evidence) Validate() error {
	if _, ok := evidenceRank[e]; !ok {
		return fmt.Errorf("unknown evidence kind %q; expected extracted, observed, declared, assumed or unresolved", string(e))
	}
	return nil
}

func (e Evidence) String() string { return string(e) }

// Strongest returns the higher-ranked of a and b in the order
// extracted > observed > declared > assumed > unresolved. An invalid value
// ranks below every valid one.
func Strongest(a, b Evidence) Evidence {
	if evidenceRank[a] >= evidenceRank[b] {
		return a
	}
	return b
}

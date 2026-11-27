package typed

// Count walks a list of identities and counts the ones that are not
// skipped: a range loop with continue, lowered to an index local and the
// element local the abstraction declares.
func (c Controller) Count(items []string, skip string) int {
	n := 0
	for _, x := range items {
		if x == skip {
			continue
		}
		n = n + 1
	}
	return n
}

// Keep collects the identities that match and stops at the first one that
// does not: append and break over list locals, and len in the result.
func (c Controller) Keep(items []string, want string) int {
	var kept []string
	for i, x := range items {
		if x != want {
			break
		}
		kept = append(kept, items[i])
	}
	return len(kept)
}

// Trim counts how many items exceed a limit: integer conversions lower to
// their operand.
func (c Controller) Trim(items []string, limit int32) int {
	n := 0
	for i := range items {
		if int32(i) >= int32(len(items))-limit {
			n = n + 1
		}
	}
	return n
}

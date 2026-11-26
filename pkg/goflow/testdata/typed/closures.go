package typed

// Budget binds a helper to a local once and calls it twice. Extraction
// inlines each call: the parameter takes the argument, and the helper's
// returns assign the local the call assigns.
func (c Controller) Budget(attempts int, ready bool) int {
	remaining := func(used int) int {
		if used >= maxRetries {
			return 0
		}
		return maxRetries - used
	}
	left := remaining(attempts)
	if ready {
		return left
	}
	more := remaining(attempts + 1)
	return more
}

// Rebound reassigns its helper, which extraction refuses: the call could
// run either body.
func (c Controller) Rebound(attempts int) int {
	score := func(used int) int { return used }
	if attempts > 1 {
		score = func(used int) int { return 0 }
	}
	result := score(attempts)
	return result
}

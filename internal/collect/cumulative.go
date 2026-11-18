package collect

import "github.com/ansonnchan/overtok/internal/normalize"

// Incrementalize converts cumulative snapshots to per-record deltas while
// keeping state isolated by source and local session identity.
func Incrementalize(observations []normalize.Observation) ([]normalize.Observation, []normalize.Warning) {
	result := make([]normalize.Observation, 0, len(observations))
	warnings := []normalize.Warning(nil)
	previousBySession := make(map[string]normalize.TokenCounts)
	for _, observation := range observations {
		if observation.CountSemantics != normalize.CountCumulative {
			result = append(result, observation)
			continue
		}
		key := cumulativeSessionKey(observation)
		previous, found := previousBySession[key]
		var previousPointer *normalize.TokenCounts
		if found {
			previousPointer = &previous
		}
		snapshot := observation.Tokens
		delta, reset := tokenDelta(snapshot, previousPointer)
		observation.Tokens = delta
		observation.CountSemantics = normalize.CountIncremental
		observation.Measurement = normalize.MeasurementDerived
		observation.SourceRecordID += "-delta"
		if reset {
			// A decrease starts a new monotonic segment; treating it as subtraction
			// would underflow and discard legitimate usage after a source reset.
			warning := normalize.Warning{Code: "counter_reset"}
			observation.Warnings = append(observation.Warnings, warning)
			warnings = append(warnings, warning)
			previousBySession[key] = cloneTokenCounts(snapshot)
		} else {
			// Missing categories remain absent from this delta but must not erase
			// the last known snapshot used when that category reappears.
			previousBySession[key] = mergeLastKnown(previous, snapshot)
		}
		if err := observation.Validate(); err != nil {
			warnings = append(warnings, normalize.Warning{Code: "invalid_cumulative_delta"})
			continue
		}
		result = append(result, observation)
	}
	return result, warnings
}

func cumulativeSessionKey(observation normalize.Observation) string {
	if observation.SessionIDLocal != nil && *observation.SessionIDLocal != "" {
		return string(observation.Source) + "\x00" + *observation.SessionIDLocal
	}
	return string(observation.Source) + "\x00record\x00" + observation.SourceRecordID
}

func tokenDelta(current normalize.TokenCounts, previous *normalize.TokenCounts) (normalize.TokenCounts, bool) {
	if previous == nil {
		return cloneTokenCounts(current), false
	}
	reset := decreased(current.InputUncached, previous.InputUncached) ||
		decreased(current.InputCacheRead, previous.InputCacheRead) || decreased(current.InputCacheWrite, previous.InputCacheWrite) ||
		decreased(current.Output, previous.Output) || decreased(current.ReasoningOutput, previous.ReasoningOutput)
	if reset {
		return cloneTokenCounts(current), true
	}
	return normalize.TokenCounts{
		InputUncached:   deltaValue(current.InputUncached, previous.InputUncached),
		InputCacheRead:  deltaValue(current.InputCacheRead, previous.InputCacheRead),
		InputCacheWrite: deltaValue(current.InputCacheWrite, previous.InputCacheWrite),
		Output:          deltaValue(current.Output, previous.Output),
		ReasoningOutput: deltaValue(current.ReasoningOutput, previous.ReasoningOutput),
	}, false
}

func decreased(current, previous *uint64) bool {
	return current != nil && previous != nil && *current < *previous
}

func deltaValue(current, previous *uint64) *uint64 {
	if current == nil {
		return nil
	}
	value := *current
	if previous != nil {
		value -= *previous
	}
	return &value
}

func mergeLastKnown(previous, current normalize.TokenCounts) normalize.TokenCounts {
	result := cloneTokenCounts(previous)
	if current.InputUncached != nil {
		result.InputUncached = cloneUint(current.InputUncached)
	}
	if current.InputCacheRead != nil {
		result.InputCacheRead = cloneUint(current.InputCacheRead)
	}
	if current.InputCacheWrite != nil {
		result.InputCacheWrite = cloneUint(current.InputCacheWrite)
	}
	if current.Output != nil {
		result.Output = cloneUint(current.Output)
	}
	if current.ReasoningOutput != nil {
		result.ReasoningOutput = cloneUint(current.ReasoningOutput)
	}
	return result
}

func cloneTokenCounts(tokens normalize.TokenCounts) normalize.TokenCounts {
	return normalize.TokenCounts{
		InputUncached: cloneUint(tokens.InputUncached), InputCacheRead: cloneUint(tokens.InputCacheRead),
		InputCacheWrite: cloneUint(tokens.InputCacheWrite), Output: cloneUint(tokens.Output),
		ReasoningOutput: cloneUint(tokens.ReasoningOutput),
	}
}

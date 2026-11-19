package collect

import (
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/ansonnchan/tokenholic/internal/normalize"
)

// ModelSummary aggregates one source, observed model, and resolution quality.
type ModelSummary struct {
	Source          normalize.Source
	ObservedModelID string
	Resolution      normalize.ModelResolution
	Tokens          normalize.TokenCounts
}

// Summarize returns deterministic per-model totals without combining different
// attribution qualities for the same observed identifier.
func Summarize(observations []normalize.Observation) ([]ModelSummary, error) {
	byModel := make(map[string]ModelSummary)
	for _, observation := range observations {
		key := strings.Join([]string{string(observation.Source), observation.ObservedModelID, string(observation.ModelResolution)}, "\x00")
		summary, found := byModel[key]
		if !found {
			byModel[key] = ModelSummary{Source: observation.Source, ObservedModelID: observation.ObservedModelID, Resolution: observation.ModelResolution, Tokens: cloneTokens(observation.Tokens)}
			continue
		}
		var err error
		summary.Tokens, err = addTokens(summary.Tokens, observation.Tokens)
		if err != nil {
			return nil, err
		}
		byModel[key] = summary
	}
	result := make([]ModelSummary, 0, len(byModel))
	for _, summary := range byModel {
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		if result[i].ObservedModelID != result[j].ObservedModelID {
			return result[i].ObservedModelID < result[j].ObservedModelID
		}
		return result[i].Resolution < result[j].Resolution
	})
	return result, nil
}

func addTokens(a, b normalize.TokenCounts) (normalize.TokenCounts, error) {
	input, err := addKnown(a.InputUncached, b.InputUncached)
	if err != nil {
		return normalize.TokenCounts{}, err
	}
	cacheRead, err := addKnown(a.InputCacheRead, b.InputCacheRead)
	if err != nil {
		return normalize.TokenCounts{}, err
	}
	cacheWrite, err := addKnown(a.InputCacheWrite, b.InputCacheWrite)
	if err != nil {
		return normalize.TokenCounts{}, err
	}
	output, err := addKnown(a.Output, b.Output)
	if err != nil {
		return normalize.TokenCounts{}, err
	}
	reasoning, err := addKnown(a.ReasoningOutput, b.ReasoningOutput)
	if err != nil {
		return normalize.TokenCounts{}, err
	}
	return normalize.TokenCounts{
		InputUncached: input, InputCacheRead: cacheRead, InputCacheWrite: cacheWrite,
		Output: output, ReasoningOutput: reasoning,
	}, nil
}

func addKnown(a, b *uint64) (*uint64, error) {
	// A sum is complete only when both contributions report the category.
	if a == nil || b == nil {
		return nil, nil
	}
	if math.MaxUint64-*a < *b {
		return nil, errors.New("model token summary overflows uint64")
	}
	value := *a + *b
	return &value, nil
}

func cloneTokens(tokens normalize.TokenCounts) normalize.TokenCounts {
	return normalize.TokenCounts{
		InputUncached: cloneUint(tokens.InputUncached), InputCacheRead: cloneUint(tokens.InputCacheRead),
		InputCacheWrite: cloneUint(tokens.InputCacheWrite), Output: cloneUint(tokens.Output), ReasoningOutput: cloneUint(tokens.ReasoningOutput),
	}
}

func cloneUint(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

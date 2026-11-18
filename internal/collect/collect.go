package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/ansonnchan/overtok/internal/adapters"
	"github.com/ansonnchan/overtok/internal/adapters/claude"
	"github.com/ansonnchan/overtok/internal/adapters/codex"
	"github.com/ansonnchan/overtok/internal/normalize"
)

type Result struct {
	Observations []normalize.Observation
	Warnings     []normalize.Warning
}

type Collector struct {
	SourceVersion string
}

func (c Collector) Collect(ctx context.Context, candidates []adapters.Candidate) Result {
	version := c.SourceVersion
	if version == "" {
		version = "local-jsonl"
	}
	result := Result{}
	rawObservations := []normalize.Observation(nil)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			result.Warnings = append(result.Warnings, normalize.Warning{Code: "collection_canceled"})
			break
		}
		if !strings.EqualFold(filepath.Ext(candidate.Path), ".jsonl") {
			result.Warnings = append(result.Warnings, normalize.Warning{Code: "unsupported_record_container"})
			continue
		}
		file, err := os.Open(candidate.Path)
		if err != nil {
			result.Warnings = append(result.Warnings, normalize.Warning{Code: "source_file_unreadable"})
			continue
		}
		switch candidate.Source {
		case normalize.SourceClaude:
			parsed, parseErr := claude.Parse(file, candidate.Surface, version)
			rawObservations = append(rawObservations, parsed.Observations...)
			result.Warnings = append(result.Warnings, parsed.Warnings...)
			if parseErr != nil {
				result.Warnings = append(result.Warnings, normalize.Warning{Code: "source_parse_failed"})
			}
		case normalize.SourceCodex:
			parsed, parseErr := codex.Parse(file, candidate.Surface, version, logicalFileID(candidate))
			rawObservations = append(rawObservations, parsed.Observations...)
			result.Warnings = append(result.Warnings, parsed.Warnings...)
			if parseErr != nil {
				result.Warnings = append(result.Warnings, normalize.Warning{Code: "source_parse_failed"})
			}
		default:
			result.Warnings = append(result.Warnings, normalize.Warning{Code: "unsupported_source"})
		}
		_ = file.Close()
	}
	rawObservations, result.Warnings = Deduplicate(rawObservations, result.Warnings)
	result.Observations, result.Warnings = incrementalizeWithWarnings(rawObservations, result.Warnings)
	sort.SliceStable(result.Warnings, func(i, j int) bool {
		if result.Warnings[i].Code != result.Warnings[j].Code {
			return result.Warnings[i].Code < result.Warnings[j].Code
		}
		return result.Warnings[i].Message < result.Warnings[j].Message
	})
	return result
}

func incrementalizeWithWarnings(observations []normalize.Observation, warnings []normalize.Warning) ([]normalize.Observation, []normalize.Warning) {
	incremental, cumulativeWarnings := Incrementalize(observations)
	return incremental, append(warnings, cumulativeWarnings...)
}

func logicalFileID(candidate adapters.Candidate) string {
	identity := candidate.RelativePath
	if identity == "" {
		identity = candidate.Path
	}
	sum := sha256.Sum256([]byte(identity))
	return "logical-file-" + hex.EncodeToString(sum[:16])
}

func Deduplicate(observations []normalize.Observation, warnings []normalize.Warning) ([]normalize.Observation, []normalize.Warning) {
	byID := make(map[string]normalize.Observation, len(observations))
	for _, observation := range observations {
		key := string(observation.Source) + "\x00" + observation.SourceRecordID
		existing, found := byID[key]
		if !found {
			byID[key] = observation
			continue
		}
		winner := preferred(existing, observation)
		if !sameUsage(existing, observation) {
			warning := normalize.Warning{Code: "duplicate_conflict"}
			winner.Warnings = append(winner.Warnings, warning)
			warnings = append(warnings, warning)
		}
		byID[key] = winner
	}
	result := make([]normalize.Observation, 0, len(byID))
	for _, observation := range byID {
		result = append(result, observation)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].OccurredAt.Equal(result[j].OccurredAt) {
			return result[i].OccurredAt.Before(result[j].OccurredAt)
		}
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].SourceRecordID < result[j].SourceRecordID
	})
	return result, warnings
}

func sameUsage(a, b normalize.Observation) bool {
	return a.Source == b.Source && a.SourceRecordID == b.SourceRecordID && a.OccurredAt.Equal(b.OccurredAt) &&
		a.ObservedModelID == b.ObservedModelID && a.ModelResolution == b.ModelResolution &&
		a.CountSemantics == b.CountSemantics && a.Measurement == b.Measurement && reflect.DeepEqual(a.Tokens, b.Tokens)
}

func preferred(a, b normalize.Observation) normalize.Observation {
	aKnown, aTotal := usageQuality(a.Tokens)
	bKnown, bTotal := usageQuality(b.Tokens)
	if aKnown != bKnown {
		if aKnown > bKnown {
			return a
		}
		return b
	}
	if aTotal != bTotal {
		if aTotal > bTotal {
			return a
		}
		return b
	}
	if !a.OccurredAt.Equal(b.OccurredAt) {
		if a.OccurredAt.After(b.OccurredAt) {
			return a
		}
		return b
	}
	aRank, bRank := surfaceRank(a.Surface), surfaceRank(b.Surface)
	if aRank != bRank {
		if aRank < bRank {
			return a
		}
		return b
	}
	if safeSignature(a) <= safeSignature(b) {
		return a
	}
	return b
}

func usageQuality(tokens normalize.TokenCounts) (int, uint64) {
	known := 0
	var total uint64
	for _, value := range []*uint64{tokens.InputUncached, tokens.InputCacheRead, tokens.InputCacheWrite, tokens.Output} {
		if value == nil {
			continue
		}
		known++
		if math.MaxUint64-total < *value {
			total = math.MaxUint64
			continue
		}
		total += *value
	}
	return known, total
}

func surfaceRank(surface normalize.Surface) int {
	switch surface {
	case normalize.SurfaceDesktopCode:
		return 0
	case normalize.SurfaceDesktopAgent:
		return 1
	case normalize.SurfaceDesktopCowork:
		return 2
	case normalize.SurfaceSharedLocal:
		return 3
	case normalize.SurfaceDesktop:
		return 4
	case normalize.SurfaceCLI:
		return 5
	default:
		return 6
	}
}

func safeSignature(observation normalize.Observation) string {
	return strings.Join([]string{
		string(observation.Surface), observation.ObservedModelID, tokenValue(observation.Tokens.InputUncached),
		tokenValue(observation.Tokens.InputCacheRead), tokenValue(observation.Tokens.InputCacheWrite),
		tokenValue(observation.Tokens.Output), tokenValue(observation.Tokens.ReasoningOutput),
	}, "\x00")
}

func tokenValue(value *uint64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprint(*value)
}

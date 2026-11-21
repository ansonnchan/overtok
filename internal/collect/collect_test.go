package collect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ansonnchan/tokenholic/internal/adapters"
	"github.com/ansonnchan/tokenholic/internal/normalize"
)

func TestCollectIsDeterministicAndDeduplicatesSurfaces(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "fixtures")
	candidates := []adapters.Candidate{
		candidate(normalize.SourceClaude, normalize.SurfaceCLI, filepath.Join(root, "claude-code", "duplicates.jsonl")),
		candidate(normalize.SourceClaude, normalize.SurfaceDesktopCode, filepath.Join(root, "claude-desktop", "code", "overlap.jsonl")),
		candidate(normalize.SourceCodex, normalize.SurfaceCLI, filepath.Join(root, "codex-cli", "cumulative.jsonl")),
		candidate(normalize.SourceCodex, normalize.SurfaceDesktop, filepath.Join(root, "codex-desktop", "overlap.jsonl")),
	}
	collector := Collector{SourceVersion: "synthetic-v1"}
	first := collector.Collect(context.Background(), candidates)
	second := collector.Collect(context.Background(), candidates)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated collection differs:\nfirst: %#v\nsecond: %#v", first, second)
	}
	if len(first.Observations) != 4 {
		t.Fatalf("observation count = %d, want 4", len(first.Observations))
	}
	var claudeCount int
	for _, observation := range first.Observations {
		if observation.Source == normalize.SourceClaude {
			claudeCount++
			if observation.Surface != normalize.SurfaceDesktopCode {
				t.Errorf("Claude duplicate winner surface = %q", observation.Surface)
			}
		}
	}
	if claudeCount != 1 {
		t.Fatalf("Claude duplicate count = %d, want 1", claudeCount)
	}
	reversed := append([]adapters.Candidate(nil), candidates...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	if got := collector.Collect(context.Background(), reversed); !reflect.DeepEqual(first, got) {
		t.Fatalf("candidate order changed collection:\nfirst: %#v\nreversed: %#v", first, got)
	}
}

func TestCodexCumulativeAttribution(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "fixtures", "codex-cli", "cumulative.jsonl")
	result := (Collector{SourceVersion: "synthetic-v1"}).Collect(context.Background(), []adapters.Candidate{
		candidate(normalize.SourceCodex, normalize.SurfaceCLI, path),
	})
	if len(result.Observations) != 3 {
		t.Fatalf("observations = %d, want 3", len(result.Observations))
	}
	summaries, err := summarize(result.Observations)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %#v", summaries)
	}
	assertTokens(t, summaries[0], "gpt-5-synthetic", 130, 30, 45)
	assertTokens(t, summaries[1], "gpt-6-synthetic", 30, 10, 15)
	for _, observation := range result.Observations {
		if observation.ModelResolution != normalize.ModelInferred || observation.CountSemantics != normalize.CountIncremental || observation.Measurement != normalize.MeasurementDerived {
			t.Fatalf("Codex attribution = %q/%q/%q", observation.ModelResolution, observation.CountSemantics, observation.Measurement)
		}
	}
}

func TestSharedCodexSurfaceRemainsAmbiguous(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "fixtures", "codex-desktop", "basic.jsonl")
	result := (Collector{}).Collect(context.Background(), []adapters.Candidate{
		candidate(normalize.SourceCodex, normalize.SurfaceSharedLocal, path),
	})
	if len(result.Observations) != 1 || result.Observations[0].Surface != normalize.SurfaceSharedLocal {
		t.Fatalf("Collect() observations = %#v", result.Observations)
	}
}

func TestCodexMidstreamOverlapIsReducedAfterDeduplication(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "fixtures")
	result := (Collector{SourceVersion: "synthetic-v1"}).Collect(context.Background(), []adapters.Candidate{
		candidate(normalize.SourceCodex, normalize.SurfaceCLI, filepath.Join(root, "codex-cli", "cumulative.jsonl")),
		candidate(normalize.SourceCodex, normalize.SurfaceDesktop, filepath.Join(root, "codex-desktop", "overlap-midstream.jsonl")),
	})
	if len(result.Observations) != 3 {
		t.Fatalf("observations = %d, want 3", len(result.Observations))
	}
	summaries, err := summarize(result.Observations)
	if err != nil {
		t.Fatal(err)
	}
	assertTokens(t, summaries[0], "gpt-5-synthetic", 130, 30, 45)
	assertTokens(t, summaries[1], "gpt-6-synthetic", 30, 10, 15)
}

func TestCodexMissingSessionUsesLogicalFileIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	data := []byte(`{"timestamp":"2026-01-04T03:00:00Z","type":"turn_context","payload":{"model":"gpt-synthetic"}}` + "\n" +
		`{"timestamp":"2026-01-04T03:01:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3}}}}` + "\n")
	paths := []string{filepath.Join(root, "one.jsonl"), filepath.Join(root, "two.jsonl")}
	candidates := make([]adapters.Candidate, 0, len(paths))
	for _, path := range paths {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, adapters.Candidate{Source: normalize.SourceCodex, Surface: normalize.SurfaceCLI, Path: path, RelativePath: filepath.Base(path)})
	}
	result := (Collector{}).Collect(context.Background(), candidates)
	if len(result.Observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(result.Observations))
	}
}

func TestCumulativeStateIsPerSessionAndKeepsLastKnownCategory(t *testing.T) {
	t.Parallel()

	a, b := "session-a", "session-b"
	input10, input7, input15 := uint64(10), uint64(7), uint64(15)
	output10, output15 := uint64(10), uint64(15)
	observations := []normalize.Observation{
		cumulativeObservation("a1", &a, 1, normalize.TokenCounts{InputUncached: &input10, Output: &output10}),
		cumulativeObservation("b1", &b, 2, normalize.TokenCounts{InputUncached: &input7}),
		cumulativeObservation("a2", &a, 3, normalize.TokenCounts{InputUncached: &input15}),
		cumulativeObservation("a3", &a, 4, normalize.TokenCounts{InputUncached: &input15, Output: &output15}),
	}
	incremental, warnings := Incrementalize(observations)
	if len(warnings) != 0 || len(incremental) != 4 {
		t.Fatalf("Incrementalize() = %#v warnings=%#v", incremental, warnings)
	}
	if got := *incremental[2].Tokens.InputUncached; got != 5 {
		t.Fatalf("session A input delta = %d, want 5", got)
	}
	if incremental[2].Tokens.Output != nil {
		t.Fatal("missing output category became known")
	}
	if got := *incremental[3].Tokens.Output; got != 5 {
		t.Fatalf("output after nil gap = %d, want 5", got)
	}
}

// A forked Codex rollout replays its parent's history under one timestamp and
// then continues the parent's running counter. Hand-computed from the two
// fork fixtures, the five distinct requests total 180 uncached input, 620
// cache read, and 70 output tokens.
func TestCodexForkReplayCountsEachRequestOnce(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "fixtures", "codex-cli")
	parent := candidate(normalize.SourceCodex, normalize.SurfaceCLI, filepath.Join(root, "fork-parent.jsonl"))
	child := candidate(normalize.SourceCodex, normalize.SurfaceCLI, filepath.Join(root, "fork-child.jsonl"))
	parent.RelativePath, child.RelativePath = "fork-parent.jsonl", "fork-child.jsonl"
	tests := []struct {
		name                     string
		candidates               []adapters.Candidate
		wantRecords              int
		input, cacheRead, output uint64
	}{
		{name: "parent then fork", candidates: []adapters.Candidate{parent, child}, wantRecords: 5, input: 180, cacheRead: 620, output: 70},
		{name: "fork then parent", candidates: []adapters.Candidate{child, parent}, wantRecords: 5, input: 180, cacheRead: 620, output: 70},
		{name: "the same fork found under two roots", candidates: []adapters.Candidate{parent, child, child}, wantRecords: 5, input: 180, cacheRead: 620, output: 70},
		// Without the parent file the replay is the only record of its two
		// requests, and they still count exactly once.
		{name: "fork alone", candidates: []adapters.Candidate{child}, wantRecords: 4, input: 160, cacheRead: 440, output: 50},
		{name: "parent alone", candidates: []adapters.Candidate{parent}, wantRecords: 3, input: 150, cacheRead: 300, output: 45},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := (Collector{SourceVersion: "synthetic-v1"}).Collect(context.Background(), tt.candidates)
			if len(result.Warnings) != 0 {
				t.Fatalf("warnings = %#v, want none: a replay is neither a counter reset nor a conflict", result.Warnings)
			}
			if len(result.Observations) != tt.wantRecords {
				t.Fatalf("observations = %d, want %d", len(result.Observations), tt.wantRecords)
			}
			summaries, err := summarize(result.Observations)
			if err != nil {
				t.Fatal(err)
			}
			if len(summaries) != 1 {
				t.Fatalf("summaries = %#v", summaries)
			}
			assertTokens(t, summaries[0], "gpt-5-synthetic", tt.input, tt.cacheRead, tt.output)
			for _, observation := range result.Observations {
				if observation.CountSemantics != normalize.CountIncremental || observation.Measurement != normalize.MeasurementReported || observation.ModelResolution != normalize.ModelInferred {
					t.Fatalf("Codex request = %q/%q/%q", observation.CountSemantics, observation.Measurement, observation.ModelResolution)
				}
			}
		})
	}

	// A replayed request keeps the time it first happened, not the fork time.
	both := (Collector{}).Collect(context.Background(), []adapters.Candidate{child, parent})
	replayedAt := time.Date(2026, 1, 7, 10, 0, 30, 0, time.UTC)
	firstRequest := time.Date(2026, 1, 7, 10, 0, 5, 0, time.UTC)
	if !both.Observations[0].OccurredAt.Equal(firstRequest) {
		t.Fatalf("first request time = %v, want %v", both.Observations[0].OccurredAt, firstRequest)
	}
	for _, observation := range both.Observations {
		if observation.OccurredAt.Equal(replayedAt) {
			t.Fatalf("a request is attributed to the replay time %v", replayedAt)
		}
	}
}

// Rollouts that carry only running totals are still reduced, and snapshots
// written under one timestamp must keep their source order: 100, then 160,
// then 200 input tokens, of which 20, 30, and 40 were cached.
func TestCumulativeSnapshotsWithOneTimestampKeepSourceOrder(t *testing.T) {
	t.Parallel()

	const stamp = `"timestamp":"2026-01-04T03:01:00Z"`
	data := `{"timestamp":"2026-01-04T03:00:00Z","type":"turn_context","payload":{"model":"gpt-5-synthetic","session_id":"synthetic-one-timestamp"}}` + "\n"
	for _, usage := range []string{
		`"input_tokens":100,"cached_input_tokens":20,"output_tokens":30`,
		`"input_tokens":160,"cached_input_tokens":30,"output_tokens":45`,
		`"input_tokens":200,"cached_input_tokens":40,"output_tokens":60`,
		`"input_tokens":260,"cached_input_tokens":70,"output_tokens":61`,
		`"input_tokens":300,"cached_input_tokens":90,"output_tokens":62`,
		`"input_tokens":310,"cached_input_tokens":95,"output_tokens":63`,
	} {
		data += `{` + stamp + `,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{` + usage + `}}}}` + "\n"
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	result := (Collector{}).Collect(context.Background(), []adapters.Candidate{candidate(normalize.SourceCodex, normalize.SurfaceCLI, path)})
	if hasWarning(result.Warnings, "counter_reset") {
		t.Fatalf("warnings = %#v: rising snapshots were treated as a counter reset", result.Warnings)
	}
	summaries, err := summarize(result.Observations)
	if err != nil {
		t.Fatal(err)
	}
	// The final snapshot is the whole usage: 310-95 uncached, 95 cached, 63 output.
	assertTokens(t, summaries[0], "gpt-5-synthetic", 215, 95, 63)
}

func TestClaudeConflictKeepsMostCompleteUsage(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "duplicates.jsonl")
	data := `{"type":"assistant","sessionId":"synthetic","requestId":"same","timestamp":"2026-01-02T03:04:05Z","message":{"model":"claude-synthetic","usage":{"input_tokens":1,"output_tokens":9}}}` + "\n" +
		`{"type":"assistant","sessionId":"synthetic","requestId":"same","timestamp":"2026-01-02T03:04:06Z","message":{"model":"claude-synthetic","usage":{"input_tokens":1,"output_tokens":10}}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	result := (Collector{}).Collect(context.Background(), []adapters.Candidate{candidate(normalize.SourceClaude, normalize.SurfaceCLI, path)})
	if len(result.Observations) != 1 || result.Observations[0].Tokens.Output == nil || *result.Observations[0].Tokens.Output != 10 || !hasWarning(result.Warnings, "duplicate_conflict") {
		t.Fatalf("Collect() = %#v", result)
	}
}

func TestSummaryPreservesResolutionStates(t *testing.T) {
	t.Parallel()

	input := uint64(1)
	reported := cumulativeObservation("reported", nil, 1, normalize.TokenCounts{InputUncached: &input})
	reported.CountSemantics = normalize.CountIncremental
	reported.ModelResolution = normalize.ModelReported
	inferred := reported
	inferred.SourceRecordID = "inferred"
	inferred.ModelResolution = normalize.ModelInferred
	summaries, err := summarize([]normalize.Observation{reported, inferred})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 || summaries[0].Resolution == summaries[1].Resolution {
		t.Fatalf("summaries = %#v", summaries)
	}
}

func TestCounterResetStartsNewSegment(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "fixtures", "codex-cli", "counter-reset.jsonl")
	result := (Collector{}).Collect(context.Background(), []adapters.Candidate{candidate(normalize.SourceCodex, normalize.SurfaceCLI, path)})
	if len(result.Observations) != 2 || !hasWarning(result.Warnings, "counter_reset") {
		t.Fatalf("Collect() = %#v", result)
	}
	summaries, err := summarize(result.Observations)
	if err != nil {
		t.Fatal(err)
	}
	summary := summaries[0]
	assertTokens(t, summary, "gpt-5-synthetic", 44, 11, 22)
}

func TestPartialAndMalformedRecordsRecover(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	partial := filepath.Join(root, "partial.jsonl")
	complete := `{"type":"assistant","sessionId":"synthetic","requestId":"one","timestamp":"2026-01-02T03:04:05Z","message":{"model":"claude-synthetic","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	truncated := `{"type":"assistant","sessionId":"synthetic"`
	if err := os.WriteFile(partial, []byte(complete+truncated), 0o600); err != nil {
		t.Fatal(err)
	}
	result := (Collector{}).Collect(context.Background(), []adapters.Candidate{candidate(normalize.SourceClaude, normalize.SurfaceCLI, partial)})
	if len(result.Observations) != 1 || !hasWarning(result.Warnings, "partial_tail") {
		t.Fatalf("Collect() = %#v", result)
	}

	malformed := filepath.Join("..", "..", "fixtures", "claude-code", "malformed-middle.jsonl")
	result = (Collector{}).Collect(context.Background(), []adapters.Candidate{candidate(normalize.SourceClaude, normalize.SurfaceCLI, malformed)})
	if len(result.Observations) != 2 || !hasWarning(result.Warnings, "malformed_record") {
		t.Fatalf("malformed Collect() = %#v", result)
	}
}

func candidate(source normalize.Source, surface normalize.Surface, path string) adapters.Candidate {
	return adapters.Candidate{Source: source, Surface: surface, Path: path}
}

func cumulativeObservation(id string, session *string, minute int, tokens normalize.TokenCounts) normalize.Observation {
	return normalize.Observation{
		Source: normalize.SourceCodex, Surface: normalize.SurfaceCLI, SourceVersion: "synthetic-v1",
		SourceRecordID: id, SessionIDLocal: session, OccurredAt: time.Date(2026, 1, 1, 0, minute, 0, 0, time.UTC),
		ObservedModelID: "gpt-synthetic", ModelResolution: normalize.ModelInferred,
		Tokens: tokens, CountSemantics: normalize.CountCumulative, Measurement: normalize.MeasurementReported,
		AdapterVersion: "1", NormalizationVersion: normalize.SchemaVersion,
		Warnings: []normalize.Warning{{Code: "model_inferred"}},
	}
}

func assertTokens(t *testing.T, summary modelSummary, model string, input, cacheRead, output uint64) {
	t.Helper()
	if summary.ObservedModelID != model || summary.Tokens.InputUncached == nil || *summary.Tokens.InputUncached != input ||
		summary.Tokens.InputCacheRead == nil || *summary.Tokens.InputCacheRead != cacheRead ||
		summary.Tokens.Output == nil || *summary.Tokens.Output != output {
		t.Fatalf("summary = %#v, want %s input=%d cache=%d output=%d", summary, model, input, cacheRead, output)
	}
}

func hasWarning(warnings []normalize.Warning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

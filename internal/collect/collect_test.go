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

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ansonnchan/overtok/internal/application"
	"github.com/ansonnchan/overtok/internal/multiplayer"
	"github.com/ansonnchan/overtok/internal/normalize"
	"github.com/ansonnchan/overtok/internal/store"
)

func TestOverviewPageLabelsPartialUnknownAndEstimates(t *testing.T) {
	t.Parallel()
	dashboard := usageDashboard(store.AvailabilityPartial, 42)
	dashboard.Today.APIEstimate = store.AggregateValue{State: store.AvailabilityUnavailable}
	dashboard.Week.APIEstimate = store.AggregateValue{State: store.AvailabilityUnavailable}
	dashboard.Last30Days.APIEstimate = store.AggregateValue{State: store.AvailabilityUnavailable}
	dashboard.AllTime.APIEstimate = store.AggregateValue{State: store.AvailabilityUnavailable}
	dashboard.Diagnostics.PendingOutbox = 3
	page := overviewPage(dashboard)
	joined := strings.Join(page.Lines, "\n")
	for _, want := range []string{
		"Total tokens: 42 (partial)",
		"API-equivalent estimate: unavailable",
		"Today includes partial or unavailable values",
		"Pending local buckets: 3",
	} {
		if want == page.Warning {
			continue
		}
		if !strings.Contains(joined, want) {
			t.Fatalf("overview lines = %q, want %q", joined, want)
		}
	}
	if page.Warning != "Today includes partial or unavailable values" || strings.Contains(joined, "$0.000000") {
		t.Fatalf("overview page = %#v", page)
	}
}

func TestModelsPagePreservesAttributionQuality(t *testing.T) {
	t.Parallel()
	value := uint64(10)
	dashboard := application.Dashboard{AllTime: store.UsageSummary{Models: []store.ModelBreakdown{
		{
			ObservedModelID: "unmapped-model", Resolution: normalize.ModelUnknown,
			Tokens:      store.TokenBreakdown{InputUncached: store.AggregateValue{Value: &value, State: store.AvailabilityComplete}},
			APIEstimate: store.AggregateValue{State: store.AvailabilityUnavailable},
		},
	}}}
	page := modelsPage(dashboard)
	joined := strings.Join(page.Lines, "\n")
	if !strings.Contains(joined, "canonical unavailable") || !strings.Contains(joined, "attribution unknown") || !strings.Contains(joined, "estimate unavailable") {
		t.Fatalf("models page = %#v", page)
	}
	if page.Warning == "" {
		t.Fatal("unknown attribution did not produce a warning")
	}
}

func TestSourcesAndSettingsRemainLocalAndExplicit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	dashboard := application.Dashboard{
		GeneratedAt: now, Timezone: "America/Vancouver",
		Diagnostics: store.DiagnosticSnapshot{
			IntegrityOK: true, SchemaVersion: 3, SupportedSchemaVersion: 3,
			AdapterHealth: []store.StoredAdapterHealth{{Health: normalize.SourceHealth{
				Source: normalize.SourceClaude, Surface: normalize.SurfaceDesktopCowork, Status: normalize.HealthPermissionDenied,
			}}},
		},
	}
	sources := sourcesPage(dashboard)
	if joined := strings.Join(sources.Lines, "\n"); !strings.Contains(joined, "records unavailable") || !strings.Contains(joined, "last success never") || sources.Warning == "" {
		t.Fatalf("sources page = %#v", sources)
	}
	settings := strings.Join(settingsPage(dashboard).Lines, "\n")
	for _, want := range []string{"No account, server, group, or synchronization", "prompts, responses, source code", "r collect and refresh", "overtok doctor"} {
		if !strings.Contains(settings, want) {
			t.Fatalf("settings page = %q, want %q", settings, want)
		}
	}
}

func TestCompleteZeroIsNotUnavailable(t *testing.T) {
	t.Parallel()
	zero := uint64(0)
	value := store.AggregateValue{Value: &zero, State: store.AvailabilityComplete}
	if got := formatValue(value); got != "0" {
		t.Fatalf("formatValue() = %q", got)
	}
	if got := formatEstimate(value); got != "$0.000000 (estimate)" {
		t.Fatalf("formatEstimate() = %q", got)
	}
}

func TestTotalTokensDoesNotDoubleCountReasoningSubset(t *testing.T) {
	t.Parallel()
	input, output, reasoning := uint64(10), uint64(7), uint64(3)
	tokens := store.TokenBreakdown{
		InputUncached:   store.AggregateValue{Value: &input, State: store.AvailabilityComplete},
		InputCacheRead:  store.AggregateValue{Value: new(uint64), State: store.AvailabilityComplete},
		InputCacheWrite: store.AggregateValue{Value: new(uint64), State: store.AvailabilityComplete},
		Output:          store.AggregateValue{Value: &output, State: store.AvailabilityComplete},
		ReasoningOutput: store.AggregateValue{Value: &reasoning, State: store.AvailabilityComplete},
	}
	if got := totalTokens(tokens); got != "17" {
		t.Fatalf("totalTokens() = %q, want 17", got)
	}
}

func TestGroupPageKeepsPartialAndEstimateMeaningVisible(t *testing.T) {
	t.Parallel()
	partial, estimate := "12345", "1500001"
	rank := 1
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	page := groupPage(multiplayer.Snapshot{
		Groups:   []multiplayer.Group{{ID: "group", Name: "Quiet", Timezone: "UTC", Role: "member"}},
		Selected: multiplayer.Group{ID: "group", Name: "Quiet", Timezone: "UTC", Role: "member"},
		Board: &multiplayer.Board{
			Period: multiplayer.PeriodToday, Metric: multiplayer.MetricAPIEstimate, GeneratedAt: now,
			Rows: []multiplayer.Row{
				{Rank: &rank, DisplayName: "Complete", Value: multiplayer.Value{Value: &estimate, State: multiplayer.AvailabilityComplete}},
				{DisplayName: "Partial", CurrentUser: true, Value: multiplayer.Value{Value: &partial, State: multiplayer.AvailabilityPartial}},
			},
		},
	})
	joined := strings.Join(page.Lines, "\n")
	for _, want := range []string{"Metric: API estimate", "$1.500001 (estimate)", "$0.012345 (estimate) (partial; unranked)", "Your standing: unranked"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("group page = %q, want %q", joined, want)
		}
	}
	if page.Warning == "" {
		t.Fatal("partial leaderboard did not warn")
	}
}

func TestCleanLineRemovesTerminalControlCharacters(t *testing.T) {
	t.Parallel()
	cleaned := cleanLine("safe\x1b[31m\nnext")
	if strings.ContainsRune(cleaned, '\x1b') || strings.ContainsRune(cleaned, '\n') || !strings.Contains(cleaned, "safe") {
		t.Fatalf("cleanLine() = %q", cleaned)
	}
}

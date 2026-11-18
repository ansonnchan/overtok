package store

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ansonnchan/overtok/internal/modelcatalog"
	"github.com/ansonnchan/overtok/internal/normalize"
)

func TestBuildUsageBucketsPreservesPartialAndUnknownPricing(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 17, 20, 1, 10, 0, time.UTC)
	first := storedObservation(start, normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 2, 3)
	second := storedObservation(start.Add(20*time.Second), normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 5, 7)
	second.Tokens.InputCacheWrite = nil
	buckets, err := BuildUsageBuckets([]StoredObservation{first, second}, 1, syntheticEstimator(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 {
		t.Fatalf("bucket count = %d, want 1", len(buckets))
	}
	bucket := buckets[0]
	if bucket.Start != start.Truncate(time.Minute) || bucket.Tokens.Output.Value == nil || *bucket.Tokens.Output.Value != 10 {
		t.Fatalf("bucket = %#v", bucket)
	}
	if bucket.Tokens.InputCacheWrite.State != AvailabilityPartial || bucket.Tokens.InputCacheWrite.Value == nil {
		t.Fatalf("cache-write coverage = %#v", bucket.Tokens.InputCacheWrite)
	}
	if bucket.APIEstimate.State != AvailabilityPartial || bucket.PricingRevision == nil {
		t.Fatalf("price coverage = %#v revision=%v", bucket.APIEstimate, bucket.PricingRevision)
	}

	unknown := storedObservation(start, normalize.SourceCodex, normalize.SurfaceCLI, "unknown-model", 1, 1)
	unknown.PricingKey = nil
	unknownBuckets, err := BuildUsageBuckets([]StoredObservation{unknown}, 1, syntheticEstimator(t))
	if err != nil {
		t.Fatal(err)
	}
	if unknownBuckets[0].APIEstimate.State != AvailabilityUnavailable || unknownBuckets[0].APIEstimate.Value != nil {
		t.Fatalf("unknown pricing = %#v", unknownBuckets[0].APIEstimate)
	}
}

func TestBucketReplayReplacementAndOutOfOrderRevision(t *testing.T) {
	t.Parallel()
	db := openStoreForTest(t)
	bucket := completeBucket(time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC), normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 10, 1)
	if result, err := db.ApplyBucketRevisions(t.Context(), []UsageBucket{bucket}); err != nil || result.Applied != 1 {
		t.Fatalf("first apply = %#v, %v", result, err)
	}
	if result, err := db.ApplyBucketRevisions(t.Context(), []UsageBucket{bucket}); err != nil || result.Ignored != 1 {
		t.Fatalf("replay = %#v, %v", result, err)
	}
	newer := bucket
	newer.Revision = 3
	setAggregate(&newer.Tokens.Output, 30, AvailabilityComplete)
	if result, err := db.ApplyBucketRevisions(t.Context(), []UsageBucket{newer}); err != nil || result.Applied != 1 {
		t.Fatalf("replacement = %#v, %v", result, err)
	}
	older := bucket
	older.Revision = 2
	setAggregate(&older.Tokens.Output, 20, AvailabilityComplete)
	if result, err := db.ApplyBucketRevisions(t.Context(), []UsageBucket{older}); err != nil || result.Ignored != 1 {
		t.Fatalf("out-of-order apply = %#v, %v", result, err)
	}
	summary, err := db.QueryUsage(t.Context(), PeriodAllTime, time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tokens.Output.Value == nil || *summary.Tokens.Output.Value != 30 {
		t.Fatalf("replacement output = %#v, want 30", summary.Tokens.Output)
	}
	outbox, err := db.PendingOutbox(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 1 || outbox[0].Revision != 3 || strings.Contains(string(outbox[0].Payload), "source_record") {
		t.Fatalf("outbox = %#v", outbox)
	}
	conflict := newer
	setAggregate(&conflict.Tokens.Output, 31, AvailabilityComplete)
	if _, err := db.ApplyBucketRevisions(t.Context(), []UsageBucket{conflict}); !errors.Is(err, ErrBucketRevisionConflict) {
		t.Fatalf("same-revision conflict error = %v", err)
	}
}

func TestUsageBucketsSurviveRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), databaseFileName)
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	bucket := completeBucket(time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC), normalize.SourceCodex, normalize.SurfaceSharedLocal, "model-a", 5, 1)
	if _, err := db.ApplyBucketRevisions(t.Context(), []UsageBucket{bucket}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	if count, err := reopened.PendingOutboxCount(t.Context()); err != nil || count != 1 {
		t.Fatalf("PendingOutboxCount() = %d, %v", count, err)
	}
	summary, err := reopened.QueryUsage(t.Context(), PeriodAllTime, time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), time.UTC)
	if err != nil || summary.Tokens.Output.Value == nil || *summary.Tokens.Output.Value != 5 {
		t.Fatalf("restart summary = %#v, %v", summary, err)
	}
}

func TestNextUsageBucketRevisionUsesStoredMaximum(t *testing.T) {
	t.Parallel()
	db := openStoreForTest(t)
	if revision, err := db.NextUsageBucketRevision(t.Context()); err != nil || revision != 1 {
		t.Fatalf("empty NextUsageBucketRevision() = %d, %v", revision, err)
	}
	start := time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC)
	buckets := []UsageBucket{
		completeBucket(start, normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 1, 3),
		completeBucket(start, normalize.SourceCodex, normalize.SurfaceSharedLocal, "model-b", 1, 9),
	}
	if _, err := db.ApplyBucketRevisions(t.Context(), buckets); err != nil {
		t.Fatal(err)
	}
	if revision, err := db.NextUsageBucketRevision(t.Context()); err != nil || revision != 10 {
		t.Fatalf("NextUsageBucketRevision() = %d, %v; want 10, nil", revision, err)
	}
	exhausted := completeBucket(start.Add(time.Minute), normalize.SourceClaude, normalize.SurfaceCLI, "model-c", 1, math.MaxUint64)
	if _, err := db.ApplyBucketRevisions(t.Context(), []UsageBucket{exhausted}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NextUsageBucketRevision(t.Context()); err == nil {
		t.Fatal("NextUsageBucketRevision() succeeded after maximum revision")
	}
}

func TestTimezoneAwarePeriodsAndBreakdowns(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("America/Vancouver")
	if err != nil {
		t.Fatal(err)
	}
	db := openStoreForTest(t)
	// March 8, 2026 starts at 08:00 UTC in Vancouver before the DST jump.
	buckets := []UsageBucket{
		completeBucket(time.Date(2026, 3, 8, 7, 59, 0, 0, time.UTC), normalize.SourceClaude, normalize.SurfaceCLI, "before", 100, 1),
		completeBucket(time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC), normalize.SourceClaude, normalize.SurfaceDesktopCode, "today-a", 3, 1),
		completeBucket(time.Date(2026, 3, 8, 9, 0, 0, 0, time.UTC), normalize.SourceCodex, normalize.SurfaceSharedLocal, "today-b", 4, 1),
	}
	if _, err := db.ApplyBucketRevisions(t.Context(), buckets); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	summary, err := db.QueryUsage(t.Context(), PeriodToday, now, location)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Start == nil || !summary.Start.Equal(time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("today start = %v", summary.Start)
	}
	if summary.Tokens.Output.Value == nil || *summary.Tokens.Output.Value != 7 {
		t.Fatalf("today output = %#v, want 7", summary.Tokens.Output)
	}
	if len(summary.Models) != 2 || len(summary.Sources) != 2 || len(summary.Surfaces) != 2 {
		t.Fatalf("breakdowns = models:%#v sources:%#v surfaces:%#v", summary.Models, summary.Sources, summary.Surfaces)
	}
	weekStart, _, err := periodBounds(PeriodWeek, time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC), location)
	if err != nil || weekStart == nil || weekStart.In(location).Weekday() != time.Monday || weekStart.In(location).Hour() != 0 {
		t.Fatalf("week start = %v, %v", weekStart, err)
	}
	thirtyStart, _, err := periodBounds(PeriodLast30Days, now, location)
	if err != nil || thirtyStart == nil || thirtyStart.In(location).Day() != 7 || thirtyStart.In(location).Month() != time.February {
		t.Fatalf("30-day start = %v, %v", thirtyStart, err)
	}
}

func TestUsageAggregationRejectsOverflow(t *testing.T) {
	t.Parallel()
	db := openStoreForTest(t)
	first := completeBucket(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), normalize.SourceClaude, normalize.SurfaceCLI, "one", math.MaxUint64, 1)
	second := completeBucket(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC), normalize.SourceClaude, normalize.SurfaceCLI, "two", 1, 1)
	if _, err := db.ApplyBucketRevisions(t.Context(), []UsageBucket{first, second}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.QueryUsage(t.Context(), PeriodAllTime, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), time.UTC); err == nil {
		t.Fatal("overflowing query succeeded")
	}
}

func storedObservation(at time.Time, source normalize.Source, surface normalize.Surface, model string, input, output uint64) StoredObservation {
	cacheRead, cacheWrite := uint64(1), uint64(1)
	provider, pricing := "synthetic", "synthetic/model"
	return StoredObservation{Source: source, Surface: surface, OccurredAt: at, ObservedModelID: model, CanonicalModelID: &model, ModelProvider: &provider, PricingKey: &pricing, ModelResolution: normalize.ModelResolved, Measurement: normalize.MeasurementReported, NormalizationVersion: "1", Tokens: normalize.TokenCounts{InputUncached: &input, InputCacheRead: &cacheRead, InputCacheWrite: &cacheWrite, Output: &output}}
}

func syntheticEstimator(t *testing.T) *modelcatalog.PricingRegistry {
	t.Helper()
	input, read, write, output := uint64(2_000_000), uint64(100_000), uint64(2_500_000), uint64(10_000_000)
	registry, err := modelcatalog.NewPricingRegistry("synthetic-pricing-r1", []modelcatalog.Price{{Key: "synthetic/model", Provider: "synthetic", Rate: modelcatalog.Rate{InputUncached: &input, InputCacheRead: &read, InputCacheWrite: &write, Output: &output}, SourceURL: "https://synthetic.invalid/pricing", SourceRevision: "synthetic-r1", EffectiveDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func completeBucket(start time.Time, source normalize.Source, surface normalize.Surface, model string, output, revision uint64) UsageBucket {
	canonical, provider, pricing, pricingRevision := model, "synthetic", "synthetic/model", "synthetic-r1"
	zero := uint64(0)
	return UsageBucket{Start: start, Source: source, Surface: surface, ObservedModelID: model, CanonicalModelID: &canonical, ModelProvider: &provider, PricingKey: &pricing, ModelResolution: normalize.ModelResolved, Measurement: normalize.MeasurementReported, NormalizationVersion: "1", Tokens: TokenBreakdown{InputUncached: AggregateValue{Value: &zero, State: AvailabilityComplete}, InputCacheRead: AggregateValue{Value: &zero, State: AvailabilityComplete}, InputCacheWrite: AggregateValue{Value: &zero, State: AvailabilityComplete}, Output: AggregateValue{Value: &output, State: AvailabilityComplete}, ReasoningOutput: AggregateValue{State: AvailabilityUnavailable}}, APIEstimate: AggregateValue{Value: &zero, State: AvailabilityComplete}, PricingRevision: &pricingRevision, Revision: revision}
}

func setAggregate(value *AggregateValue, number uint64, state Availability) {
	value.Value = &number
	value.State = state
}

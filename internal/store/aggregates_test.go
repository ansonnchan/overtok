package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ansonnchan/tokenholic/internal/modelcatalog"
	"github.com/ansonnchan/tokenholic/internal/normalize"
)

func TestBuildUsageBucketsPreservesPartialAndUnknownPricing(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 17, 20, 1, 10, 0, time.UTC)
	first := storedObservation(start, normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 2, 3)
	second := storedObservation(start.Add(20*time.Second), normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 5, 7)
	second.Tokens.InputCacheWrite = nil
	buckets, err := BuildUsageBuckets([]StoredObservation{first, second}, 1, syntheticPreviewer(t))
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
	preview := bucket.Preview
	if preview.APIEstimate.State != AvailabilityPartial || preview.CatalogRevision != "synthetic-r1" ||
		preview.CanonicalModelID == nil || *preview.CanonicalModelID != "model-a" || preview.Resolution != normalize.ModelReported {
		t.Fatalf("preview = %#v", preview)
	}
	// 7 input + 2 cache read + 1 cache write + 10 output, all with a rate.
	if preview.PricedTokens != 20 || preview.UnpricedTokens != 0 {
		t.Fatalf("priced/unpriced tokens = %d/%d, want 20/0", preview.PricedTokens, preview.UnpricedTokens)
	}

	unknown := storedObservation(start, normalize.SourceCodex, normalize.SurfaceCLI, "unknown-model", 1, 1)
	unknown.ModelResolution = normalize.ModelInferred
	unknownBuckets, err := BuildUsageBuckets([]StoredObservation{unknown}, 1, syntheticPreviewer(t))
	if err != nil {
		t.Fatal(err)
	}
	unknownPreview := unknownBuckets[0].Preview
	if unknownPreview.APIEstimate.State != AvailabilityUnavailable || unknownPreview.APIEstimate.Value != nil ||
		unknownPreview.UnpricedReason != modelcatalog.ReasonUnknownModel || unknownPreview.UnpricedTokens != 4 {
		t.Fatalf("unknown pricing = %#v", unknownPreview)
	}
	// The upload still says how the source attributed the model.
	if unknownBuckets[0].ModelEvidence != normalize.ModelInferred || unknownPreview.Resolution != normalize.ModelUnknown {
		t.Fatalf("evidence/resolution = %q/%q", unknownBuckets[0].ModelEvidence, unknownPreview.Resolution)
	}
}

func TestUsageBucketPayloadCarriesObservedEvidenceOnly(t *testing.T) {
	t.Parallel()
	db := openStoreForTest(t)
	start := time.Date(2026, 11, 17, 20, 1, 0, 0, time.UTC)
	buckets, err := BuildUsageBuckets([]StoredObservation{
		storedObservation(start, normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 2, 3),
	}, 1, syntheticPreviewer(t))
	if err != nil {
		t.Fatal(err)
	}
	if buckets[0].Preview.APIEstimate.Value == nil {
		t.Fatal("test bucket has no preview estimate to leak")
	}
	if _, err := db.ApplyBucketRevisions(t.Context(), buckets); err != nil {
		t.Fatal(err)
	}
	outbox, err := db.PendingOutbox(t.Context())
	if err != nil || len(outbox) != 1 {
		t.Fatalf("outbox = %#v, %v", outbox, err)
	}
	var payload struct {
		BucketKey string                     `json:"bucket_key"`
		Bucket    map[string]json.RawMessage `json:"bucket"`
	}
	if err := json.Unmarshal(outbox[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	want := []string{"measurement", "model_evidence", "normalization_version", "observed_model_id", "revision", "source", "start", "surface", "tokens"}
	got := make([]string, 0, len(payload.Bucket))
	for field := range payload.Bucket {
		got = append(got, field)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("uploaded bucket fields = %v, want exactly %v", got, want)
	}
	for _, leaked := range []string{"canonical", "provider", "pricing", "estimate", "microusd", "priced", "catalog_revision"} {
		if strings.Contains(string(outbox[0].Payload), leaked) {
			t.Fatalf("upload payload contains client-derived %q: %s", leaked, outbox[0].Payload)
		}
	}
}

func TestUnchangedBucketKeepsItsRevisionAndStaysAcknowledged(t *testing.T) {
	t.Parallel()
	db := openStoreForTest(t)
	start := time.Date(2026, 11, 17, 20, 1, 0, 0, time.UTC)
	observations := []StoredObservation{storedObservation(start, normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 2, 3)}
	if result, err := db.rebuildForTest(t, observations, 1, syntheticPreviewer(t)); err != nil || result.Applied != 1 {
		t.Fatalf("first rebuild = %#v, %v", result, err)
	}
	outbox, err := db.PendingOutbox(t.Context())
	if err != nil || len(outbox) != 1 {
		t.Fatalf("outbox = %#v, %v", outbox, err)
	}
	if _, err := db.AcknowledgeOutbox(t.Context(), []OutboxAcknowledgement{{BucketKey: outbox[0].BucketKey, Revision: 1}}); err != nil {
		t.Fatal(err)
	}

	// A later refresh proposes a higher revision for identical content, and a
	// newer bundled catalog changes only the local preview.
	repriced := syntheticPreviewerAt(t, "synthetic-r2", 20_000_000)
	if result, err := db.rebuildForTest(t, observations, 2, repriced); err != nil || result.Applied != 0 || result.Ignored != 1 {
		t.Fatalf("unchanged rebuild = %#v, %v", result, err)
	}
	if count, err := db.PendingOutboxCount(t.Context()); err != nil || count != 0 {
		t.Fatalf("pending after unchanged rebuild = %d, %v; want nothing to upload", count, err)
	}
	if revision, err := db.NextUsageBucketRevision(t.Context()); err != nil || revision != 2 {
		t.Fatalf("next revision = %d, %v; want 2 because the bucket kept revision 1", revision, err)
	}
	summary, err := db.QueryUsage(t.Context(), PeriodAllTime, start.Add(time.Hour), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	// 2 input*$2 + 1 read*$0.10 + 1 write*$2.50 + 3 output*$20 = 66.6, floored
	// to 66 micro-USD. At the old $10 output rate it was 36.
	if summary.APIEstimate.Value == nil || *summary.APIEstimate.Value != 66 {
		t.Fatalf("refreshed preview estimate = %#v", summary.APIEstimate)
	}

	// Changed usage does upload again, at the new revision.
	observations = append(observations, storedObservation(start.Add(time.Second), normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 1, 1))
	if result, err := db.rebuildForTest(t, observations, 2, repriced); err != nil || result.Applied != 1 {
		t.Fatalf("changed rebuild = %#v, %v", result, err)
	}
	outbox, err = db.PendingOutbox(t.Context())
	if err != nil || len(outbox) != 1 || outbox[0].Revision != 2 {
		t.Fatalf("outbox after change = %#v, %v", outbox, err)
	}
}

func TestBucketEstimateUsesCacheWriteLifetimes(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 17, 20, 1, 0, 0, time.UTC)
	value := func(v uint64) *uint64 { return &v }
	// Synthetic rates per million: $2 input, $0.10 read, $2.50 base write,
	// $4 one-hour write, $10 output. Each case writes a million cache tokens.
	tests := []struct {
		name                string
		fiveMinute, oneHour *uint64
		wantMicroUSD        uint64
		wantState           Availability
	}{
		{name: "one-hour writes", fiveMinute: value(0), oneHour: value(1_000_000), wantMicroUSD: 4_000_000, wantState: AvailabilityComplete},
		{name: "five-minute writes", fiveMinute: value(1_000_000), oneHour: value(0), wantMicroUSD: 2_500_000, wantState: AvailabilityComplete},
		{name: "unknown lifetime is a lower bound", wantMicroUSD: 2_500_000, wantState: AvailabilityPartial},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observation := storedObservation(start, normalize.SourceClaude, normalize.SurfaceCLI, "model-a", 0, 0)
			observation.Tokens.InputCacheRead = value(0)
			observation.Tokens.InputCacheWrite = value(1_000_000)
			observation.Tokens.InputCacheWrite5m, observation.Tokens.InputCacheWrite1h = tt.fiveMinute, tt.oneHour
			buckets, err := BuildUsageBuckets([]StoredObservation{observation}, 1, syntheticPreviewer(t))
			if err != nil {
				t.Fatal(err)
			}
			estimate := buckets[0].Preview.APIEstimate
			if estimate.Value == nil || *estimate.Value != tt.wantMicroUSD || estimate.State != tt.wantState {
				t.Fatalf("estimate = %#v, want %d micro-USD %s", estimate, tt.wantMicroUSD, tt.wantState)
			}
		})
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
	if count, err := db.AcknowledgeOutbox(t.Context(), []OutboxAcknowledgement{{BucketKey: outbox[0].BucketKey, Revision: 2}}); err != nil || count != 0 {
		t.Fatalf("stale acknowledgement = %d, %v", count, err)
	}
	if count, err := db.AcknowledgeOutbox(t.Context(), []OutboxAcknowledgement{{BucketKey: outbox[0].BucketKey, Revision: 3}}); err != nil || count != 1 {
		t.Fatalf("current acknowledgement = %d, %v", count, err)
	}
	if count, err := db.PendingOutboxCount(t.Context()); err != nil || count != 0 {
		t.Fatalf("pending after acknowledgement = %d, %v", count, err)
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
	return StoredObservation{
		Source: source, Surface: surface, OccurredAt: at, ObservedModelID: model,
		ModelResolution: normalize.ModelReported, Measurement: normalize.MeasurementReported, NormalizationVersion: "1",
		Tokens: normalize.TokenCounts{InputUncached: &input, InputCacheRead: &cacheRead, InputCacheWrite: &cacheWrite, Output: &output},
	}
}

// syntheticPreviewer prices "model-a" at $2 input, $0.10 cache read, $2.50
// base cache write, $4 one-hour cache write, and $10 output per million.
func syntheticPreviewer(t *testing.T) *modelcatalog.Deriver {
	t.Helper()
	return syntheticPreviewerAt(t, "synthetic-r1", 10_000_000)
}

func syntheticPreviewerAt(t *testing.T, revision string, outputRate uint64) *modelcatalog.Deriver {
	t.Helper()
	input, read, write, writeHour := uint64(2_000_000), uint64(100_000), uint64(2_500_000), uint64(4_000_000)
	registry, err := modelcatalog.NewPricingRegistry(revision, []modelcatalog.Price{{
		Key: "synthetic/model-a", Provider: "synthetic",
		Rate:      modelcatalog.Rate{InputUncached: &input, InputCacheRead: &read, InputCacheWrite: &write, InputCacheWrite1h: &writeHour, Output: &outputRate},
		SourceURL: "https://synthetic.invalid/pricing", SourceRevision: "synthetic-r1", EffectiveDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	deriver, err := modelcatalog.NewDeriver(modelcatalog.Catalog{
		Revision: revision,
		Models:   []modelcatalog.Model{{CanonicalID: "model-a", Provider: "synthetic", PricingKey: "synthetic/model-a"}},
	}, registry)
	if err != nil {
		t.Fatal(err)
	}
	return deriver
}

func (db *DB) rebuildForTest(t *testing.T, observations []StoredObservation, revision uint64, previewer Previewer) (BucketApplyResult, error) {
	t.Helper()
	buckets, err := BuildUsageBuckets(observations, revision, previewer)
	if err != nil {
		return BucketApplyResult{}, err
	}
	return db.ApplyBucketRevisions(t.Context(), buckets)
}

func completeBucket(start time.Time, source normalize.Source, surface normalize.Surface, model string, output, revision uint64) UsageBucket {
	zero := uint64(0)
	complete := AggregateValue{Value: &zero, State: AvailabilityComplete}
	return UsageBucket{
		Start: start, Source: source, Surface: surface, ObservedModelID: model,
		ModelEvidence: normalize.ModelReported, Measurement: normalize.MeasurementReported, NormalizationVersion: "1",
		Tokens: TokenBreakdown{
			InputUncached: complete, InputCacheRead: complete, InputCacheWrite: complete,
			InputCacheWrite5m: complete, InputCacheWrite1h: complete,
			Output: AggregateValue{Value: &output, State: AvailabilityComplete}, ReasoningOutput: AggregateValue{State: AvailabilityUnavailable},
		},
		Preview:  Preview{Resolution: normalize.ModelUnknown, APIEstimate: AggregateValue{State: AvailabilityUnavailable}},
		Revision: revision,
	}
}

func setAggregate(value *AggregateValue, number uint64, state Availability) {
	value.Value = &number
	value.State = state
}

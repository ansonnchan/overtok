package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ansonnchan/overtok/internal/modelcatalog"
	"github.com/ansonnchan/overtok/internal/normalize"
)

const bucketInterval = time.Minute

// Availability distinguishes complete, partial, unavailable, and empty values.
type Availability string

const (
	// AvailabilityEmpty means the query period has no contributing buckets.
	AvailabilityEmpty Availability = "empty"
	// AvailabilityComplete means every contributing record reported the value.
	AvailabilityComplete Availability = "complete"
	// AvailabilityPartial means the value sums known records but some were unavailable.
	AvailabilityPartial Availability = "partial"
	// AvailabilityUnavailable means records exist but none report the value.
	AvailabilityUnavailable Availability = "unavailable"
)

// AggregateValue is a value plus its coverage state; nil is never reported as zero.
type AggregateValue struct {
	Value *uint64      `json:"value"`
	State Availability `json:"state"`
}

// TokenBreakdown holds billing categories plus reasoning subset metadata.
// ReasoningOutput must not be added to Output when calculating total tokens.
type TokenBreakdown struct {
	InputUncached   AggregateValue `json:"input_uncached"`
	InputCacheRead  AggregateValue `json:"input_cache_read"`
	InputCacheWrite AggregateValue `json:"input_cache_write"`
	Output          AggregateValue `json:"output"`
	ReasoningOutput AggregateValue `json:"reasoning_output"`
}

// UsageBucket is an absolute one-minute UTC usage snapshot.
type UsageBucket struct {
	Start                time.Time                 `json:"start"`
	Source               normalize.Source          `json:"source"`
	Surface              normalize.Surface         `json:"surface"`
	ObservedModelID      string                    `json:"observed_model_id"`
	CanonicalModelID     *string                   `json:"canonical_model_id"`
	ModelProvider        *string                   `json:"model_provider"`
	PricingKey           *string                   `json:"pricing_key"`
	ModelResolution      normalize.ModelResolution `json:"model_resolution"`
	Measurement          normalize.Measurement     `json:"measurement"`
	NormalizationVersion string                    `json:"normalization_version"`
	Tokens               TokenBreakdown            `json:"tokens"`
	APIEstimate          AggregateValue            `json:"api_equivalent_estimate_microusd"`
	PricingRevision      *string                   `json:"pricing_revision"`
	Revision             uint64                    `json:"revision"`
}

// PriceEstimator provides versioned API-equivalent estimates.
type PriceEstimator interface {
	EstimateTokens(string, normalize.TokenCounts) (modelcatalog.Estimate, bool, error)
}

// BucketApplyResult reports newer replacements and ignored replays.
type BucketApplyResult struct {
	Applied int
	Ignored int
}

// OutboxRecord is one pending allowlisted absolute bucket revision.
type OutboxRecord struct {
	BucketKey string
	Revision  uint64
	Payload   json.RawMessage
}

// OutboxAcknowledgement marks one exact bucket revision as durable remotely.
type OutboxAcknowledgement struct {
	BucketKey string
	Revision  uint64
}

// Period names one local calendar query window.
type Period string

const (
	// PeriodToday starts at midnight in the requested location.
	PeriodToday Period = "today"
	// PeriodWeek starts at Monday midnight in the requested location.
	PeriodWeek Period = "week"
	// PeriodLast30Days includes today and the preceding 29 local dates.
	PeriodLast30Days Period = "last_30_days"
	// PeriodAllTime includes every bucket through the current UTC minute.
	PeriodAllTime Period = "all_time"
)

// ModelBreakdown preserves observed and canonical attribution quality.
type ModelBreakdown struct {
	ObservedModelID  string
	CanonicalModelID *string
	Resolution       normalize.ModelResolution
	Tokens           TokenBreakdown
	APIEstimate      AggregateValue
}

// NamedBreakdown is one source or surface aggregate.
type NamedBreakdown struct {
	Name        string
	Tokens      TokenBreakdown
	APIEstimate AggregateValue
}

// UsageSummary is a timezone-aware local period query.
type UsageSummary struct {
	Period      Period
	Start       *time.Time
	End         time.Time
	Tokens      TokenBreakdown
	APIEstimate AggregateValue
	Models      []ModelBreakdown
	Sources     []NamedBreakdown
	Surfaces    []NamedBreakdown
}

// BuildUsageBuckets deterministically reduces observations to absolute UTC buckets.
func BuildUsageBuckets(observations []StoredObservation, revision uint64, estimator PriceEstimator) ([]UsageBucket, error) {
	if revision == 0 {
		return nil, errors.New("usage bucket revision must be positive")
	}
	groups := make(map[string]*bucketAccumulator)
	for _, observation := range observations {
		start := observation.OccurredAt.UTC().Truncate(bucketInterval)
		key := bucketGroupKey(start, observation)
		accumulator := groups[key]
		if accumulator == nil {
			accumulator = newBucketAccumulator(start, observation, revision)
			groups[key] = accumulator
		}
		if err := accumulator.addObservation(observation, estimator); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	buckets := make([]UsageBucket, 0, len(keys))
	for _, key := range keys {
		buckets = append(buckets, groups[key].bucket())
	}
	return buckets, nil
}

// ApplyBucketRevisions replaces older absolute values and enqueues only the newest revision.
func (db *DB) ApplyBucketRevisions(ctx context.Context, buckets []UsageBucket) (BucketApplyResult, error) {
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return BucketApplyResult{}, fmt.Errorf("begin usage bucket update: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	ordered := append([]UsageBucket(nil), buckets...)
	sort.Slice(ordered, func(i, j int) bool {
		left, right := usageBucketKey(ordered[i]), usageBucketKey(ordered[j])
		if left != right {
			return left < right
		}
		return ordered[i].Revision < ordered[j].Revision
	})
	var result BucketApplyResult
	for _, bucket := range ordered {
		applied, err := applyBucket(ctx, tx, bucket)
		if err != nil {
			return BucketApplyResult{}, err
		}
		if applied {
			result.Applied++
		} else {
			result.Ignored++
		}
	}
	if err := tx.Commit(); err != nil {
		return BucketApplyResult{}, fmt.Errorf("commit usage bucket update: %w", err)
	}
	committed = true
	return result, nil
}

// RebuildUsageBuckets reads committed observations and applies one absolute revision.
func (db *DB) RebuildUsageBuckets(ctx context.Context, revision uint64, estimator PriceEstimator) (BucketApplyResult, error) {
	observations, err := db.Observations(ctx)
	if err != nil {
		return BucketApplyResult{}, err
	}
	buckets, err := BuildUsageBuckets(observations, revision, estimator)
	if err != nil {
		return BucketApplyResult{}, err
	}
	return db.ApplyBucketRevisions(ctx, buckets)
}

// NextUsageBucketRevision returns a monotonic revision after every stored
// bucket, including values outside SQLite's signed integer range.
func (db *DB) NextUsageBucketRevision(ctx context.Context) (uint64, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT revision FROM usage_buckets`)
	if err != nil {
		return 0, fmt.Errorf("query usage bucket revisions: %w", err)
	}
	defer rows.Close()
	var maximum uint64
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return 0, fmt.Errorf("decode usage bucket revision row: %w", err)
		}
		revision, err := strconv.ParseUint(encoded, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("decode usage bucket revision: %w", err)
		}
		if revision > maximum {
			maximum = revision
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate usage bucket revisions: %w", err)
	}
	if maximum == math.MaxUint64 {
		return 0, errors.New("usage bucket revision is exhausted")
	}
	return maximum + 1, nil
}

// PendingOutbox returns current pending absolute revisions without network behavior.
func (db *DB) PendingOutbox(ctx context.Context) ([]OutboxRecord, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT bucket_key, revision, payload_json FROM outbox_records WHERE state = 'pending' ORDER BY bucket_key`)
	if err != nil {
		return nil, fmt.Errorf("query local outbox: %w", err)
	}
	defer rows.Close()
	var records []OutboxRecord
	for rows.Next() {
		var record OutboxRecord
		var revision, payload string
		if err := rows.Scan(&record.BucketKey, &revision, &payload); err != nil {
			return nil, fmt.Errorf("decode local outbox: %w", err)
		}
		parsed, err := strconv.ParseUint(revision, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decode outbox revision: %w", err)
		}
		record.Revision = parsed
		record.Payload = json.RawMessage(payload)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate local outbox: %w", err)
	}
	return records, nil
}

// AcknowledgeOutbox marks only the exact revisions confirmed by the server.
// A newer local replacement remains pending if an older request completes late.
func (db *DB) AcknowledgeOutbox(ctx context.Context, acknowledgements []OutboxAcknowledgement) (int64, error) {
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, fmt.Errorf("begin outbox acknowledgement: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var acknowledged int64
	for _, acknowledgement := range acknowledgements {
		if len(acknowledgement.BucketKey) != 64 || acknowledgement.Revision == 0 {
			return 0, errors.New("outbox acknowledgement has invalid identity")
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE outbox_records SET state = 'acknowledged'
			WHERE bucket_key = ? AND revision = ? AND state = 'pending'
		`, acknowledgement.BucketKey, strconv.FormatUint(acknowledgement.Revision, 10))
		if err != nil {
			return 0, fmt.Errorf("acknowledge outbox revision: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("count acknowledged outbox revision: %w", err)
		}
		acknowledged += changed
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit outbox acknowledgement: %w", err)
	}
	committed = true
	return acknowledged, nil
}

// PendingOutboxCount returns the number of newest revisions awaiting future sync.
func (db *DB) PendingOutboxCount(ctx context.Context) (int, error) {
	var count int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_records WHERE state = 'pending'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count local outbox: %w", err)
	}
	return count, nil
}

// QueryUsage aggregates buckets for a local calendar period in location.
func (db *DB) QueryUsage(ctx context.Context, period Period, now time.Time, location *time.Location) (UsageSummary, error) {
	start, end, err := periodBounds(period, now, location)
	if err != nil {
		return UsageSummary{}, err
	}
	query := `SELECT payload_json FROM usage_buckets WHERE bucket_start < ?`
	args := []any{formatTime(end)}
	if start != nil {
		query += ` AND bucket_start >= ?`
		args = append(args, formatTime(*start))
	}
	query += ` ORDER BY bucket_start, bucket_key`
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return UsageSummary{}, fmt.Errorf("query usage buckets: %w", err)
	}
	defer rows.Close()
	accumulator := newSummaryAccumulator(period, start, end)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return UsageSummary{}, fmt.Errorf("decode usage bucket row: %w", err)
		}
		var stored bucketPayload
		if err := json.Unmarshal(payload, &stored); err != nil {
			return UsageSummary{}, fmt.Errorf("decode usage bucket payload: %w", err)
		}
		if err := accumulator.add(stored.Bucket); err != nil {
			return UsageSummary{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return UsageSummary{}, fmt.Errorf("iterate usage buckets: %w", err)
	}
	return accumulator.summary(), nil
}

type valueAccumulator struct {
	seen, known, missing bool
	sum                  uint64
}

func (a *valueAccumulator) add(value *uint64) error {
	a.seen = true
	if value == nil {
		a.missing = true
		return nil
	}
	a.known = true
	if math.MaxUint64-a.sum < *value {
		return errors.New("usage aggregation overflows uint64")
	}
	a.sum += *value
	return nil
}

func (a *valueAccumulator) addAggregate(value AggregateValue) error {
	if value.State == AvailabilityEmpty {
		return nil
	}
	a.seen = true
	if value.Value != nil {
		a.known = true
		if math.MaxUint64-a.sum < *value.Value {
			return errors.New("usage aggregation overflows uint64")
		}
		a.sum += *value.Value
	}
	if value.State != AvailabilityComplete {
		a.missing = true
	}
	return nil
}

func (a valueAccumulator) value() AggregateValue {
	if !a.seen {
		return AggregateValue{State: AvailabilityEmpty}
	}
	if !a.known {
		return AggregateValue{State: AvailabilityUnavailable}
	}
	value := a.sum
	if a.missing {
		return AggregateValue{Value: &value, State: AvailabilityPartial}
	}
	return AggregateValue{Value: &value, State: AvailabilityComplete}
}

type tokenAccumulator struct{ input, read, write, output, reasoning valueAccumulator }

func (a *tokenAccumulator) add(tokens normalize.TokenCounts) error {
	for _, pair := range []struct {
		accumulator *valueAccumulator
		value       *uint64
	}{{&a.input, tokens.InputUncached}, {&a.read, tokens.InputCacheRead}, {&a.write, tokens.InputCacheWrite}, {&a.output, tokens.Output}, {&a.reasoning, tokens.ReasoningOutput}} {
		if err := pair.accumulator.add(pair.value); err != nil {
			return err
		}
	}
	return nil
}

func (a *tokenAccumulator) addBreakdown(tokens TokenBreakdown) error {
	for _, pair := range []struct {
		accumulator *valueAccumulator
		value       AggregateValue
	}{{&a.input, tokens.InputUncached}, {&a.read, tokens.InputCacheRead}, {&a.write, tokens.InputCacheWrite}, {&a.output, tokens.Output}, {&a.reasoning, tokens.ReasoningOutput}} {
		if err := pair.accumulator.addAggregate(pair.value); err != nil {
			return err
		}
	}
	return nil
}

func (a tokenAccumulator) value() TokenBreakdown {
	return TokenBreakdown{a.input.value(), a.read.value(), a.write.value(), a.output.value(), a.reasoning.value()}
}

type bucketAccumulator struct {
	template        UsageBucket
	tokens          tokenAccumulator
	estimate        valueAccumulator
	pricingRevision string
}

func newBucketAccumulator(start time.Time, observation StoredObservation, revision uint64) *bucketAccumulator {
	return &bucketAccumulator{template: UsageBucket{Start: start, Source: observation.Source, Surface: observation.Surface, ObservedModelID: observation.ObservedModelID, CanonicalModelID: cloneString(observation.CanonicalModelID), ModelProvider: cloneString(observation.ModelProvider), PricingKey: cloneString(observation.PricingKey), ModelResolution: observation.ModelResolution, Measurement: observation.Measurement, NormalizationVersion: observation.NormalizationVersion, Revision: revision}}
}

func (a *bucketAccumulator) addObservation(observation StoredObservation, estimator PriceEstimator) error {
	if err := a.tokens.add(observation.Tokens); err != nil {
		return err
	}
	if estimator == nil || observation.PricingKey == nil {
		return a.estimate.add(nil)
	}
	estimate, available, err := estimator.EstimateTokens(*observation.PricingKey, observation.Tokens)
	if err != nil {
		return err
	}
	if !available {
		return a.estimate.add(nil)
	}
	a.pricingRevision = estimate.PricingRevision
	value := estimate.MicroUSD
	return a.estimate.add(&value)
}

func (a bucketAccumulator) bucket() UsageBucket {
	bucket := a.template
	bucket.Tokens = a.tokens.value()
	bucket.APIEstimate = a.estimate.value()
	if a.pricingRevision != "" {
		revision := a.pricingRevision
		bucket.PricingRevision = &revision
	}
	return bucket
}

type bucketPayload struct {
	BucketKey string      `json:"bucket_key"`
	Bucket    UsageBucket `json:"bucket"`
}

// ErrBucketRevisionConflict marks different payloads claiming the same revision.
var ErrBucketRevisionConflict = errors.New("usage bucket revision conflicts with stored payload")

func applyBucket(ctx context.Context, tx *sql.Tx, bucket UsageBucket) (bool, error) {
	if err := validateBucket(bucket); err != nil {
		return false, err
	}
	key := usageBucketKey(bucket)
	payload, err := json.Marshal(bucketPayload{BucketKey: key, Bucket: bucket})
	if err != nil {
		return false, fmt.Errorf("encode usage bucket: %w", err)
	}
	var currentRevision, currentPayload string
	err = tx.QueryRowContext(ctx, `SELECT revision, payload_json FROM usage_buckets WHERE bucket_key = ?`, key).Scan(&currentRevision, &currentPayload)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read usage bucket revision: %w", err)
	}
	if err == nil {
		current, parseErr := strconv.ParseUint(currentRevision, 10, 64)
		if parseErr != nil {
			return false, fmt.Errorf("decode usage bucket revision: %w", parseErr)
		}
		if bucket.Revision < current {
			return false, nil
		}
		if bucket.Revision == current {
			if currentPayload != string(payload) {
				return false, ErrBucketRevisionConflict
			}
			return false, nil
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_buckets(
		bucket_key,bucket_start,interval_seconds,source,surface,observed_model_id,canonical_model_id,model_provider,pricing_key,model_resolution,measurement,normalization_version,
		input_uncached_tokens,input_uncached_state,input_cache_read_tokens,input_cache_read_state,input_cache_write_tokens,input_cache_write_state,output_tokens,output_state,reasoning_output_tokens,reasoning_output_state,
		api_estimate_microusd,api_estimate_state,pricing_revision,revision,payload_json
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(bucket_key) DO UPDATE SET
		input_uncached_tokens=excluded.input_uncached_tokens,input_uncached_state=excluded.input_uncached_state,input_cache_read_tokens=excluded.input_cache_read_tokens,input_cache_read_state=excluded.input_cache_read_state,
		input_cache_write_tokens=excluded.input_cache_write_tokens,input_cache_write_state=excluded.input_cache_write_state,output_tokens=excluded.output_tokens,output_state=excluded.output_state,
		reasoning_output_tokens=excluded.reasoning_output_tokens,reasoning_output_state=excluded.reasoning_output_state,api_estimate_microusd=excluded.api_estimate_microusd,api_estimate_state=excluded.api_estimate_state,
		pricing_revision=excluded.pricing_revision,revision=excluded.revision,payload_json=excluded.payload_json,updated_at=CURRENT_TIMESTAMP`,
		key, formatTime(bucket.Start), 60, bucket.Source, bucket.Surface, bucket.ObservedModelID, bucket.CanonicalModelID, bucket.ModelProvider, bucket.PricingKey, bucket.ModelResolution, bucket.Measurement, bucket.NormalizationVersion,
		aggregateSQL(bucket.Tokens.InputUncached), bucket.Tokens.InputUncached.State, aggregateSQL(bucket.Tokens.InputCacheRead), bucket.Tokens.InputCacheRead.State, aggregateSQL(bucket.Tokens.InputCacheWrite), bucket.Tokens.InputCacheWrite.State,
		aggregateSQL(bucket.Tokens.Output), bucket.Tokens.Output.State, aggregateSQL(bucket.Tokens.ReasoningOutput), bucket.Tokens.ReasoningOutput.State, aggregateSQL(bucket.APIEstimate), bucket.APIEstimate.State, bucket.PricingRevision, strconv.FormatUint(bucket.Revision, 10), string(payload))
	if err != nil {
		return false, fmt.Errorf("write usage bucket: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO outbox_records(bucket_key,revision,payload_json,state) VALUES (?,?,?,'pending') ON CONFLICT(bucket_key) DO UPDATE SET revision=excluded.revision,payload_json=excluded.payload_json,state='pending',created_at=CURRENT_TIMESTAMP`, key, strconv.FormatUint(bucket.Revision, 10), string(payload))
	if err != nil {
		return false, fmt.Errorf("enqueue usage bucket revision: %w", err)
	}
	return true, nil
}

func validateBucket(bucket UsageBucket) error {
	if bucket.Revision == 0 || bucket.Start.Location() != time.UTC || !bucket.Start.Equal(bucket.Start.Truncate(bucketInterval)) {
		return errors.New("usage bucket requires a positive revision and aligned UTC start")
	}
	for _, value := range []AggregateValue{bucket.Tokens.InputUncached, bucket.Tokens.InputCacheRead, bucket.Tokens.InputCacheWrite, bucket.Tokens.Output, bucket.Tokens.ReasoningOutput, bucket.APIEstimate} {
		if value.State != AvailabilityComplete && value.State != AvailabilityPartial && value.State != AvailabilityUnavailable {
			return errors.New("usage bucket has invalid availability")
		}
		if value.State == AvailabilityComplete && value.Value == nil || value.State == AvailabilityUnavailable && value.Value != nil {
			return errors.New("usage bucket availability contradicts its value")
		}
	}
	return nil
}

func aggregateSQL(value AggregateValue) any {
	if value.Value == nil {
		return nil
	}
	return strconv.FormatUint(*value.Value, 10)
}

func usageBucketKey(bucket UsageBucket) string {
	parts := []string{formatTime(bucket.Start), string(bucket.Source), string(bucket.Surface), bucket.ObservedModelID, pointerText(bucket.CanonicalModelID), pointerText(bucket.ModelProvider), pointerText(bucket.PricingKey), string(bucket.ModelResolution), string(bucket.Measurement), bucket.NormalizationVersion}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func bucketGroupKey(start time.Time, observation StoredObservation) string {
	return strings.Join([]string{formatTime(start), string(observation.Source), string(observation.Surface), observation.ObservedModelID, pointerText(observation.CanonicalModelID), pointerText(observation.ModelProvider), pointerText(observation.PricingKey), string(observation.ModelResolution), string(observation.Measurement), observation.NormalizationVersion}, "\x00")
}
func pointerText(value *string) string {
	if value == nil {
		return "<null>"
	}
	return *value
}
func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func periodBounds(period Period, now time.Time, location *time.Location) (*time.Time, time.Time, error) {
	if location == nil {
		return nil, time.Time{}, errors.New("usage period location is required")
	}
	local := now.In(location)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	end := now.UTC().Truncate(bucketInterval).Add(bucketInterval)
	var start time.Time
	switch period {
	case PeriodToday:
		start = day
	case PeriodWeek:
		days := (int(day.Weekday()) + 6) % 7
		start = day.AddDate(0, 0, -days)
	case PeriodLast30Days:
		start = day.AddDate(0, 0, -29)
	case PeriodAllTime:
		return nil, end, nil
	default:
		return nil, time.Time{}, fmt.Errorf("unsupported usage period %q", period)
	}
	utc := start.UTC()
	return &utc, end, nil
}

type summaryAccumulator struct {
	period            Period
	start             *time.Time
	end               time.Time
	all               summaryValues
	models            map[string]*modelSummaryAccumulator
	sources, surfaces map[string]*summaryValues
}
type summaryValues struct {
	tokens   tokenAccumulator
	estimate valueAccumulator
}
type modelSummaryAccumulator struct {
	key    ModelBreakdown
	values summaryValues
}

func newSummaryAccumulator(period Period, start *time.Time, end time.Time) *summaryAccumulator {
	return &summaryAccumulator{period: period, start: start, end: end, models: map[string]*modelSummaryAccumulator{}, sources: map[string]*summaryValues{}, surfaces: map[string]*summaryValues{}}
}
func (a *summaryAccumulator) add(bucket UsageBucket) error {
	if err := addSummaryValues(&a.all, bucket); err != nil {
		return err
	}
	mkey := strings.Join([]string{bucket.ObservedModelID, pointerText(bucket.CanonicalModelID), string(bucket.ModelResolution)}, "\x00")
	model := a.models[mkey]
	if model == nil {
		model = &modelSummaryAccumulator{key: ModelBreakdown{ObservedModelID: bucket.ObservedModelID, CanonicalModelID: cloneString(bucket.CanonicalModelID), Resolution: bucket.ModelResolution}}
		a.models[mkey] = model
	}
	if err := addSummaryValues(&model.values, bucket); err != nil {
		return err
	}
	for name, collection := range map[string]map[string]*summaryValues{string(bucket.Source): a.sources, string(bucket.Surface): a.surfaces} {
		values := collection[name]
		if values == nil {
			values = &summaryValues{}
			collection[name] = values
		}
		if err := addSummaryValues(values, bucket); err != nil {
			return err
		}
	}
	return nil
}
func addSummaryValues(values *summaryValues, bucket UsageBucket) error {
	if err := values.tokens.addBreakdown(bucket.Tokens); err != nil {
		return err
	}
	return values.estimate.addAggregate(bucket.APIEstimate)
}
func (a *summaryAccumulator) summary() UsageSummary {
	result := UsageSummary{Period: a.period, Start: a.start, End: a.end, Tokens: a.all.tokens.value(), APIEstimate: a.all.estimate.value()}
	modelKeys := sortedMapKeys(a.models)
	for _, key := range modelKeys {
		entry := a.models[key]
		entry.key.Tokens = entry.values.tokens.value()
		entry.key.APIEstimate = entry.values.estimate.value()
		result.Models = append(result.Models, entry.key)
	}
	for _, pair := range []struct {
		source map[string]*summaryValues
		target *[]NamedBreakdown
	}{{a.sources, &result.Sources}, {a.surfaces, &result.Surfaces}} {
		for _, name := range sortedMapKeys(pair.source) {
			values := pair.source[name]
			*pair.target = append(*pair.target, NamedBreakdown{Name: name, Tokens: values.tokens.value(), APIEstimate: values.estimate.value()})
		}
	}
	return result
}
func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ansonnchan/tokenholic/internal/store"
)

const (
	// SchemaVersion is the current upload contract version.
	SchemaVersion = 1
	// MaxBatchBuckets bounds one upload request.
	MaxBatchBuckets = 100
)

// Value preserves both a nullable unsigned value and its coverage state.
type Value struct {
	Value *uint64 `json:"value"`
	State string  `json:"state"`
}

// Tokens is the allowlisted normalized token breakdown. ReasoningOutput is a
// subset of Output and must not be added to it.
type Tokens struct {
	InputUncached   Value `json:"input_uncached"`
	InputCacheRead  Value `json:"input_cache_read"`
	InputCacheWrite Value `json:"input_cache_write"`
	Output          Value `json:"output"`
	ReasoningOutput Value `json:"reasoning_output"`
}

// Bucket is one absolute versioned minute of privacy-limited usage.
type Bucket struct {
	Start                time.Time `json:"start"`
	Source               string    `json:"source"`
	Surface              string    `json:"surface"`
	ObservedModelID      string    `json:"observed_model_id"`
	CanonicalModelID     *string   `json:"canonical_model_id"`
	ModelProvider        *string   `json:"model_provider"`
	PricingKey           *string   `json:"pricing_key"`
	ModelResolution      string    `json:"model_resolution"`
	Measurement          string    `json:"measurement"`
	NormalizationVersion string    `json:"normalization_version"`
	Tokens               Tokens    `json:"tokens"`
	APIEstimate          Value     `json:"api_equivalent_estimate_microusd"`
	PricingRevision      *string   `json:"pricing_revision"`
	Revision             uint64    `json:"revision"`
}

// Envelope gives a deterministic identity to one bucket.
type Envelope struct {
	BucketKey string `json:"bucket_key"`
	Bucket    Bucket `json:"bucket"`
}

// Batch is one bounded upload request.
type Batch struct {
	SchemaVersion int        `json:"schema_version"`
	Buckets       []Envelope `json:"buckets"`
}

// AckStatus describes how the server treated one absolute revision.
type AckStatus string

const (
	AckApplied   AckStatus = "applied"
	AckUnchanged AckStatus = "unchanged"
	AckStale     AckStatus = "stale"
)

// Acknowledgement identifies a durable server decision for one revision.
type Acknowledgement struct {
	BucketKey string    `json:"bucket_key"`
	Revision  uint64    `json:"revision"`
	Status    AckStatus `json:"status"`
}

// BatchResponse acknowledges every accepted, repeated, or stale revision.
type BatchResponse struct {
	Acknowledgements []Acknowledgement `json:"acknowledgements"`
}

// BuildBatch decodes the exact local outbox serialization into the explicit
// upload allowlist and returns deterministic request JSON.
func BuildBatch(records []store.OutboxRecord) (Batch, []byte, error) {
	if len(records) == 0 || len(records) > MaxBatchBuckets {
		return Batch{}, nil, fmt.Errorf("sync batch must contain 1 through %d buckets", MaxBatchBuckets)
	}
	batch := Batch{SchemaVersion: SchemaVersion, Buckets: make([]Envelope, 0, len(records))}
	for _, record := range records {
		decoder := json.NewDecoder(bytes.NewReader(record.Payload))
		decoder.DisallowUnknownFields()
		var envelope Envelope
		if err := decoder.Decode(&envelope); err != nil {
			return Batch{}, nil, fmt.Errorf("decode outbox bucket %q: %w", record.BucketKey, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return Batch{}, nil, fmt.Errorf("decode outbox bucket %q: trailing JSON", record.BucketKey)
		}
		if envelope.BucketKey != record.BucketKey || envelope.Bucket.Revision != record.Revision {
			return Batch{}, nil, fmt.Errorf("outbox bucket %q identity differs from its payload", record.BucketKey)
		}
		if err := envelope.Validate(); err != nil {
			return Batch{}, nil, fmt.Errorf("validate outbox bucket %q: %w", record.BucketKey, err)
		}
		batch.Buckets = append(batch.Buckets, envelope)
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return Batch{}, nil, fmt.Errorf("encode sync batch: %w", err)
	}
	return batch, body, nil
}

// Validate checks the bounded protocol and every bucket invariant.
func (b Batch) Validate() error {
	if b.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported sync schema version %d", b.SchemaVersion)
	}
	if len(b.Buckets) == 0 || len(b.Buckets) > MaxBatchBuckets {
		return fmt.Errorf("sync batch must contain 1 through %d buckets", MaxBatchBuckets)
	}
	seen := make(map[string]struct{}, len(b.Buckets))
	for _, envelope := range b.Buckets {
		if err := envelope.Validate(); err != nil {
			return err
		}
		if _, exists := seen[envelope.BucketKey]; exists {
			return fmt.Errorf("bucket %q is duplicated", envelope.BucketKey)
		}
		seen[envelope.BucketKey] = struct{}{}
	}
	return nil
}

// Validate checks identity, source/model semantics, and nullable coverage.
func (e Envelope) Validate() error {
	if len(e.BucketKey) != 64 {
		return errors.New("bucket key must be a 64-character SHA-256 digest")
	}
	if _, err := hex.DecodeString(e.BucketKey); err != nil || strings.ToLower(e.BucketKey) != e.BucketKey {
		return errors.New("bucket key must be lowercase hexadecimal")
	}
	bucket := e.Bucket
	if bucket.Revision == 0 || bucket.Start.Location() != time.UTC || !bucket.Start.Equal(bucket.Start.Truncate(time.Minute)) {
		return errors.New("bucket requires a positive revision and minute-aligned UTC start")
	}
	if !validSourceSurface(bucket.Source, bucket.Surface) {
		return errors.New("bucket has an invalid source and surface")
	}
	if len(bucket.ObservedModelID) < 1 || len(bucket.ObservedModelID) > 256 || len(bucket.NormalizationVersion) < 1 || len(bucket.NormalizationVersion) > 64 {
		return errors.New("bucket model or normalization version is invalid")
	}
	if bucket.Measurement != "reported" && bucket.Measurement != "derived" && bucket.Measurement != "estimated" {
		return errors.New("bucket measurement is invalid")
	}
	switch bucket.ModelResolution {
	case "unknown":
		if bucket.CanonicalModelID != nil || bucket.ModelProvider != nil || bucket.PricingKey != nil {
			return errors.New("unknown model includes resolved metadata")
		}
	case "reported", "resolved", "inferred":
		if invalidText(bucket.CanonicalModelID, 256) || invalidText(bucket.ModelProvider, 64) {
			return errors.New("resolved model is missing canonical metadata")
		}
		if bucket.PricingKey != nil && invalidText(bucket.PricingKey, 256) {
			return errors.New("bucket pricing key is invalid")
		}
	default:
		return errors.New("bucket model resolution is invalid")
	}
	for _, value := range []Value{
		bucket.Tokens.InputUncached, bucket.Tokens.InputCacheRead, bucket.Tokens.InputCacheWrite,
		bucket.Tokens.Output, bucket.Tokens.ReasoningOutput, bucket.APIEstimate,
	} {
		if err := validateValue(value); err != nil {
			return err
		}
	}
	if bucket.Tokens.ReasoningOutput.Value != nil && bucket.Tokens.Output.Value != nil && *bucket.Tokens.ReasoningOutput.Value > *bucket.Tokens.Output.Value {
		return errors.New("reasoning output exceeds total output")
	}
	if bucket.APIEstimate.Value != nil && invalidText(bucket.PricingRevision, 64) {
		return errors.New("available API estimate is missing its pricing revision")
	}
	if expected := bucketKey(bucket); expected != e.BucketKey {
		return errors.New("bucket key does not match its dimensions")
	}
	return nil
}

// CanonicalEnvelope returns deterministic client-evidence bytes for server
// conflict detection. Server-derived pricing is excluded from the identity.
func CanonicalEnvelope(envelope Envelope) ([]byte, [32]byte, error) {
	if err := envelope.Validate(); err != nil {
		return nil, [32]byte{}, err
	}
	canonical := envelope
	canonical.Bucket.APIEstimate = Value{State: "unavailable"}
	canonical.Bucket.PricingRevision = nil
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return encoded, sha256.Sum256(encoded), nil
}

func validSourceSurface(source, surface string) bool {
	return source == "claude" && (surface == "cli" || surface == "desktop_code" || surface == "desktop_agent" || surface == "desktop_cowork") ||
		source == "codex" && (surface == "cli" || surface == "desktop" || surface == "shared_local")
}

func validateValue(value Value) error {
	switch value.State {
	case "complete", "partial":
		if value.Value == nil {
			return errors.New("available value is null")
		}
	case "unavailable":
		if value.Value != nil {
			return errors.New("unavailable value is non-null")
		}
	default:
		return errors.New("bucket availability is invalid")
	}
	return nil
}

func invalidText(value *string, maximum int) bool {
	return value == nil || len(*value) < 1 || len(*value) > maximum
}

func bucketKey(bucket Bucket) string {
	parts := []string{
		bucket.Start.UTC().Format(time.RFC3339Nano), bucket.Source, bucket.Surface,
		bucket.ObservedModelID, pointerText(bucket.CanonicalModelID), pointerText(bucket.ModelProvider),
		pointerText(bucket.PricingKey), bucket.ModelResolution, bucket.Measurement, bucket.NormalizationVersion,
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func pointerText(value *string) string {
	if value == nil {
		return "<null>"
	}
	return *value
}

package normalize

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestObservationValidateModelResolution(t *testing.T) {
	t.Parallel()

	canonical := "claude-sonnet"
	tests := []struct {
		name    string
		mutate  func(*Observation)
		wantErr string
	}{
		{name: "reported"},
		{name: "resolved", mutate: func(o *Observation) { o.ModelResolution = ModelResolved; o.CanonicalModelID = &canonical }},
		{name: "inferred", mutate: func(o *Observation) {
			o.ModelResolution = ModelInferred
			o.Warnings = []Warning{{Code: "model_inferred"}}
		}},
		{name: "unknown", mutate: func(o *Observation) { o.ModelResolution = ModelUnknown; o.ObservedModelID = "" }},
		{name: "reported missing ID", mutate: func(o *Observation) { o.ObservedModelID = "" }, wantErr: "observed identifier"},
		{name: "resolved missing canonical", mutate: func(o *Observation) { o.ModelResolution = ModelResolved }, wantErr: "canonical"},
		{name: "inferred missing warning", mutate: func(o *Observation) { o.ModelResolution = ModelInferred }, wantErr: "model_inferred"},
		{name: "unknown with canonical", mutate: func(o *Observation) { o.ModelResolution = ModelUnknown; o.CanonicalModelID = &canonical }, wantErr: "cannot have canonical"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			observation := validObservation()
			if tt.mutate != nil {
				tt.mutate(&observation)
			}
			err := observation.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestTokenCountsPreserveUnknownAndZero(t *testing.T) {
	t.Parallel()

	zero := uint64(0)
	counts := TokenCounts{InputUncached: &zero}
	data, err := json.Marshal(counts)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"input_uncached_tokens":0`) || !strings.Contains(got, `"output_tokens":null`) {
		t.Fatalf("Marshal() = %s, want explicit zero and null", got)
	}
}

func TestKnownTotalDoesNotDoubleCountReasoning(t *testing.T) {
	t.Parallel()

	input, output, reasoning := uint64(10), uint64(7), uint64(3)
	total, err := (TokenCounts{InputUncached: &input, Output: &output, ReasoningOutput: &reasoning}).KnownTotal()
	if err != nil {
		t.Fatal(err)
	}
	if total == nil || *total != 17 {
		t.Fatalf("KnownTotal() = %v, want 17", total)
	}
}

func TestKnownTotalOverflow(t *testing.T) {
	t.Parallel()

	max, one := uint64(math.MaxUint64), uint64(1)
	if _, err := (TokenCounts{InputUncached: &max, Output: &one}).KnownTotal(); err == nil {
		t.Fatal("KnownTotal() error = nil, want overflow")
	}
}

func TestCapabilitiesDistinguishUnsupported(t *testing.T) {
	t.Parallel()

	capabilities := Capabilities{
		Model: CapabilityInferred, InputUncached: CapabilityReported,
		InputCacheRead: CapabilityUnsupported, InputCacheWrite: CapabilityUnsupported,
		Output: CapabilityReported, ReasoningOutput: CapabilityDerived,
	}
	if err := capabilities.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if capabilities.InputCacheRead != CapabilityUnsupported {
		t.Fatal("unsupported capability was not preserved")
	}
}

func validObservation() Observation {
	input := uint64(1)
	return Observation{
		Source:               SourceClaude,
		Surface:              SurfaceCLI,
		SourceVersion:        "synthetic-v1",
		SourceRecordID:       "record-1",
		OccurredAt:           time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ObservedModelID:      "claude-synthetic",
		ModelResolution:      ModelReported,
		Tokens:               TokenCounts{InputUncached: &input},
		CountSemantics:       CountIncremental,
		Measurement:          MeasurementReported,
		AdapterVersion:       "1",
		NormalizationVersion: SchemaVersion,
	}
}

func TestCacheWriteLifetimesValidateAgainstTheCombinedTotal(t *testing.T) {
	t.Parallel()

	value := func(v uint64) *uint64 { return &v }
	tests := []struct {
		name        string
		tokens      TokenCounts
		wantErr     string
		wantUnknown *uint64
	}{
		{name: "combined only has an unknown time-to-live", tokens: TokenCounts{InputCacheWrite: value(40)}, wantUnknown: value(40)},
		{name: "complete split", tokens: TokenCounts{InputCacheWrite: value(30), InputCacheWrite5m: value(10), InputCacheWrite1h: value(20)}, wantUnknown: value(0)},
		{name: "one lifetime", tokens: TokenCounts{InputCacheWrite: value(30), InputCacheWrite1h: value(20)}, wantUnknown: value(10)},
		{name: "no cache-write evidence", tokens: TokenCounts{Output: value(1)}},
		{name: "split without total", tokens: TokenCounts{Output: value(1), InputCacheWrite5m: value(1)}, wantErr: "require the combined total"},
		{name: "split above total", tokens: TokenCounts{InputCacheWrite: value(5), InputCacheWrite5m: value(3), InputCacheWrite1h: value(3)}, wantErr: "cannot exceed"},
		{name: "split overflow", tokens: TokenCounts{InputCacheWrite: value(5), InputCacheWrite5m: value(math.MaxUint64), InputCacheWrite1h: value(1)}, wantErr: "overflow"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			observation := validObservation()
			observation.Tokens = tt.tokens
			err := observation.Validate()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			got := tt.tokens.CacheWriteUnknownTTL()
			if (got == nil) != (tt.wantUnknown == nil) || got != nil && *got != *tt.wantUnknown {
				t.Fatalf("CacheWriteUnknownTTL() = %v, want %v", got, tt.wantUnknown)
			}
		})
	}
}

func TestKnownTotalCountsCacheWritesOnce(t *testing.T) {
	t.Parallel()

	write, fiveMinute, oneHour, output := uint64(30), uint64(10), uint64(20), uint64(5)
	total, err := (TokenCounts{InputCacheWrite: &write, InputCacheWrite5m: &fiveMinute, InputCacheWrite1h: &oneHour, Output: &output}).KnownTotal()
	if err != nil {
		t.Fatal(err)
	}
	if total == nil || *total != 35 {
		t.Fatalf("KnownTotal() = %v, want 35", total)
	}
}

package sync

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ansonnchan/tokenholic/internal/store"
)

func TestBuildBatchUsesExplicitPrivacyAllowlist(t *testing.T) {
	envelope := sampleEnvelope(1, 7)
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	batch, body, err := BuildBatch([]store.OutboxRecord{{
		BucketKey: envelope.BucketKey, Revision: envelope.Bucket.Revision, Payload: payload,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if batch.SchemaVersion != SchemaVersion || len(batch.Buckets) != 1 {
		t.Fatalf("batch = %#v", batch)
	}
	for _, prohibited := range []string{"prompt", "response", "source_code", "file_path", "project_name", "repository", "branch_name", "command_text", "provider_credential", "session_id"} {
		if strings.Contains(strings.ToLower(string(body)), prohibited) {
			t.Fatalf("serialized batch contains prohibited field %q: %s", prohibited, body)
		}
	}
	var decoded Batch
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.Buckets[0].Bucket.Revision != 7 {
		t.Fatalf("encoded batch = %#v, %v", decoded, err)
	}
}

func TestBuildBatchRejectsIdentityAndAvailabilityContradictions(t *testing.T) {
	envelope := sampleEnvelope(1, 1)
	tests := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{name: "key", mutate: func(value *Envelope) { value.BucketKey = strings.Repeat("a", 64) }},
		{name: "unknown metadata", mutate: func(value *Envelope) { value.Bucket.ModelResolution = "unknown" }},
		{name: "missing complete value", mutate: func(value *Envelope) { value.Bucket.Tokens.Output.Value = nil }},
		{name: "reasoning exceeds output", mutate: func(value *Envelope) {
			reasoning := uint64(8)
			value.Bucket.Tokens.ReasoningOutput = Value{Value: &reasoning, State: "complete"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := envelope
			test.mutate(&changed)
			payload, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := BuildBatch([]store.OutboxRecord{{BucketKey: changed.BucketKey, Revision: changed.Bucket.Revision, Payload: payload}}); err == nil {
				t.Fatal("BuildBatch() accepted contradictory payload")
			}
		})
	}
}

func sampleEnvelope(minute int, revision uint64) Envelope {
	zero, input, output, cost := uint64(0), uint64(3), uint64(7), uint64(11)
	canonical, provider, pricingKey, pricingRevision := "synthetic-model", "synthetic", "synthetic/model", "synthetic-r1"
	bucket := Bucket{
		Start:  time.Date(2026, 11, 18, 12, minute, 0, 0, time.UTC),
		Source: "claude", Surface: "cli", ObservedModelID: "synthetic-model",
		CanonicalModelID: &canonical, ModelProvider: &provider, PricingKey: &pricingKey,
		ModelResolution: "resolved", Measurement: "reported", NormalizationVersion: "1",
		Tokens: Tokens{
			InputUncached:   Value{Value: &input, State: "complete"},
			InputCacheRead:  Value{Value: &zero, State: "complete"},
			InputCacheWrite: Value{Value: &zero, State: "complete"},
			Output:          Value{Value: &output, State: "complete"},
			ReasoningOutput: Value{State: "unavailable"},
		},
		APIEstimate: Value{Value: &cost, State: "complete"}, PricingRevision: &pricingRevision,
		Revision: revision,
	}
	return Envelope{BucketKey: bucketKey(bucket), Bucket: bucket}
}

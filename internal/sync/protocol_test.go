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
	count := func(v uint64) Value { return Value{Value: &v, State: "complete"} }
	rekey := func(value *Envelope) { value.BucketKey = bucketKey(value.Bucket) }
	tests := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{name: "key", mutate: func(value *Envelope) { value.BucketKey = strings.Repeat("a", 64) }},
		{name: "evidence changed without rekeying", mutate: func(value *Envelope) { value.Bucket.ModelEvidence = "inferred" }},
		{name: "catalog result offered as evidence", mutate: func(value *Envelope) { value.Bucket.ModelEvidence = "resolved"; rekey(value) }},
		{name: "attributed model without an identifier", mutate: func(value *Envelope) { value.Bucket.ObservedModelID = ""; rekey(value) }},
		{name: "unsupported source", mutate: func(value *Envelope) { value.Bucket.Source = "chat"; rekey(value) }},
		{name: "missing complete value", mutate: func(value *Envelope) { value.Bucket.Tokens.Output.Value = nil }},
		{name: "reasoning exceeds output", mutate: func(value *Envelope) { value.Bucket.Tokens.ReasoningOutput = count(8) }},
		{name: "lifetimes without a total", mutate: func(value *Envelope) {
			value.Bucket.Tokens.InputCacheWrite = Value{State: "unavailable"}
			value.Bucket.Tokens.InputCacheWrite5m = count(1)
		}},
		{name: "lifetimes exceed the total", mutate: func(value *Envelope) {
			value.Bucket.Tokens.InputCacheWrite = count(5)
			value.Bucket.Tokens.InputCacheWrite5m, value.Bucket.Tokens.InputCacheWrite1h = count(3), count(3)
		}},
		{name: "lifetimes overflow", mutate: func(value *Envelope) {
			value.Bucket.Tokens.InputCacheWrite = count(5)
			value.Bucket.Tokens.InputCacheWrite5m, value.Bucket.Tokens.InputCacheWrite1h = count(^uint64(0)), count(1)
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

func TestEnvelopeAcceptsEveryEvidenceShape(t *testing.T) {
	count := func(v uint64) Value { return Value{Value: &v, State: "complete"} }
	tests := []struct {
		name   string
		mutate func(*Bucket)
	}{
		{name: "reported model"},
		{name: "model no catalog has heard of", mutate: func(bucket *Bucket) { bucket.ObservedModelID = "model-released-tomorrow" }},
		{name: "inferred model", mutate: func(bucket *Bucket) {
			bucket.Source, bucket.Surface, bucket.ModelEvidence = "codex", "shared_local", "inferred"
		}},
		{name: "no model at all", mutate: func(bucket *Bucket) { bucket.ObservedModelID, bucket.ModelEvidence = "", "unknown" }},
		{name: "combined cache write only", mutate: func(bucket *Bucket) {
			bucket.Tokens.InputCacheWrite = count(40)
			bucket.Tokens.InputCacheWrite5m, bucket.Tokens.InputCacheWrite1h = Value{State: "unavailable"}, Value{State: "unavailable"}
		}},
		{name: "partly split cache write", mutate: func(bucket *Bucket) {
			bucket.Tokens.InputCacheWrite = count(50)
			bucket.Tokens.InputCacheWrite5m, bucket.Tokens.InputCacheWrite1h = Value{State: "unavailable"}, Value{Value: count(30).Value, State: "partial"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bucket := sampleEnvelope(1, 1).Bucket
			if test.mutate != nil {
				test.mutate(&bucket)
			}
			if err := (Envelope{BucketKey: bucketKey(bucket), Bucket: bucket}).Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

// The request body is the whole protocol surface. This pins its exact field
// set so a client conclusion can never be added to an upload unnoticed.
func TestBatchWireFormatIsObservedEvidenceOnly(t *testing.T) {
	envelope := sampleEnvelope(1, 7)
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := BuildBatch([]store.OutboxRecord{{BucketKey: envelope.BucketKey, Revision: 7, Payload: payload}})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema_version":2,"buckets":[{"bucket_key":"` + "%s" + `","bucket":{` +
		`"start":"2026-11-18T12:01:00Z","source":"claude","surface":"cli","observed_model_id":"synthetic-model",` +
		`"model_evidence":"reported","measurement":"reported","normalization_version":"1","tokens":{` +
		`"input_uncached":{"value":3,"state":"complete"},"input_cache_read":{"value":0,"state":"complete"},` +
		`"input_cache_write":{"value":9,"state":"complete"},"input_cache_write_5m":{"value":4,"state":"complete"},` +
		`"input_cache_write_1h":{"value":5,"state":"complete"},"output":{"value":7,"state":"complete"},` +
		`"reasoning_output":{"value":null,"state":"unavailable"}},"revision":7}}]}`
	if got := string(body); got != strings.Replace(want, "%s", envelope.BucketKey, 1) {
		t.Fatalf("request body =\n%s\nwant\n%s", got, strings.Replace(want, "%s", envelope.BucketKey, 1))
	}
}

func TestCanonicalEnvelopeIdentifiesClientEvidence(t *testing.T) {
	envelope := sampleEnvelope(1, 1)
	_, originalHash, err := CanonicalEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	_, repeatedHash, err := CanonicalEnvelope(sampleEnvelope(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if originalHash != repeatedHash {
		t.Fatal("identical evidence produced different identities")
	}

	changedUsage := envelope
	otherOutput := uint64(8)
	changedUsage.Bucket.Tokens.Output.Value = &otherOutput
	_, usageHash, err := CanonicalEnvelope(changedUsage)
	if err != nil {
		t.Fatal(err)
	}
	if originalHash == usageHash {
		t.Fatal("usage change did not change client-evidence identity")
	}
}

func sampleEnvelope(minute int, revision uint64) Envelope {
	zero, input, output := uint64(0), uint64(3), uint64(7)
	write, fiveMinute, oneHour := uint64(9), uint64(4), uint64(5)
	bucket := Bucket{
		Start:  time.Date(2026, 11, 18, 12, minute, 0, 0, time.UTC),
		Source: "claude", Surface: "cli", ObservedModelID: "synthetic-model",
		ModelEvidence: "reported", Measurement: "reported", NormalizationVersion: "1",
		Tokens: Tokens{
			InputUncached:     Value{Value: &input, State: "complete"},
			InputCacheRead:    Value{Value: &zero, State: "complete"},
			InputCacheWrite:   Value{Value: &write, State: "complete"},
			InputCacheWrite5m: Value{Value: &fiveMinute, State: "complete"},
			InputCacheWrite1h: Value{Value: &oneHour, State: "complete"},
			Output:            Value{Value: &output, State: "complete"},
			ReasoningOutput:   Value{State: "unavailable"},
		},
		Revision: revision,
	}
	return Envelope{BucketKey: bucketKey(bucket), Bucket: bucket}
}

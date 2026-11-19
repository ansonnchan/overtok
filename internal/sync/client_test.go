package sync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ansonnchan/tokenholic/internal/store"
)

func TestClientRetriesBatchesAndAcknowledgesExactRevisions(t *testing.T) {
	outbox := &fakeOutbox{records: outboxRecords(t, MaxBatchBuckets+1)}
	requests := 0
	transport := syncRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Header.Get("Authorization") != "Bearer device-credential" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		if requests == 1 {
			return syncResponse(request, http.StatusServiceUnavailable, "{}", nil), nil
		}
		var batch Batch
		if err := json.NewDecoder(request.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		response := BatchResponse{Acknowledgements: make([]Acknowledgement, 0, len(batch.Buckets))}
		for _, envelope := range batch.Buckets {
			response.Acknowledgements = append(response.Acknowledgements, Acknowledgement{
				BucketKey: envelope.BucketKey, Revision: envelope.Bucket.Revision, Status: AckApplied,
			})
		}
		encoded, _ := json.Marshal(response)
		return syncResponse(request, http.StatusOK, string(encoded), nil), nil
	})
	client, err := NewClient("https://api.example.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	client.sleep = func(_ context.Context, duration time.Duration) error { waits = append(waits, duration); return nil }
	client.jitter = func(time.Duration) time.Duration { return 0 }
	summary, err := client.Run(t.Context(), outbox, "device-credential", false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 || len(waits) != 1 || summary.Batches != 2 || summary.Buckets != 101 || summary.Acknowledged != 101 || len(outbox.acknowledged) != 101 {
		t.Fatalf("sync requests/waits/summary/acks = %d/%v/%#v/%d", requests, waits, summary, len(outbox.acknowledged))
	}
}

func TestClientDryRunUsesRequestSerializationWithoutNetwork(t *testing.T) {
	outbox := &fakeOutbox{records: outboxRecords(t, MaxBatchBuckets+1)}
	transport := syncRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("dry run made a network request")
		return nil, nil
	})
	client, err := NewClient("https://api.example.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	summary, err := client.Run(t.Context(), outbox, "device-credential", true, &output)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 || summary.Batches != 2 || summary.Acknowledged != 0 || len(outbox.acknowledged) != 0 {
		t.Fatalf("dry run = lines:%d summary:%#v acks:%d", len(lines), summary, len(outbox.acknowledged))
	}
	for _, line := range lines {
		var batch Batch
		if err := json.Unmarshal([]byte(line), &batch); err != nil || batch.Validate() != nil {
			t.Fatalf("dry-run body = %q, %v", line, err)
		}
	}
}

func outboxRecords(t *testing.T, count int) []store.OutboxRecord {
	t.Helper()
	records := make([]store.OutboxRecord, 0, count)
	for index := 0; index < count; index++ {
		envelope := sampleEnvelope(index, uint64(index+1))
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, store.OutboxRecord{BucketKey: envelope.BucketKey, Revision: envelope.Bucket.Revision, Payload: payload})
	}
	return records
}

type fakeOutbox struct {
	records      []store.OutboxRecord
	acknowledged []store.OutboxAcknowledgement
}

func (f *fakeOutbox) PendingOutbox(context.Context) ([]store.OutboxRecord, error) {
	return append([]store.OutboxRecord(nil), f.records...), nil
}
func (f *fakeOutbox) AcknowledgeOutbox(_ context.Context, values []store.OutboxAcknowledgement) (int64, error) {
	f.acknowledged = append(f.acknowledged, values...)
	return int64(len(values)), nil
}

type syncRoundTripFunc func(*http.Request) (*http.Response, error)

func (function syncRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func syncResponse(request *http.Request, status int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

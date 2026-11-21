package sync

import (
	"context"
	"encoding/json"
	"errors"
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
		if request.Method != http.MethodPost || request.URL.Path != "/v2/sync/buckets" {
			t.Fatalf("request = %s %s, want POST /v2/sync/buckets", request.Method, request.URL.Path)
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
	summary, err := client.Run(t.Context(), outbox, "device-credential")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 || len(waits) != 1 || summary.Batches != 2 || summary.Buckets != 101 || summary.Acknowledged != 101 || len(outbox.acknowledged) != 101 {
		t.Fatalf("sync requests/waits/summary/acks = %d/%v/%#v/%d", requests, waits, summary, len(outbox.acknowledged))
	}
}

func TestClientDryRunUsesRequestSerializationWithoutNetwork(t *testing.T) {
	outbox := &fakeOutbox{records: outboxRecords(t, MaxBatchBuckets+1)}
	var output strings.Builder
	summary, err := DryRun(t.Context(), outbox, &output)
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

// show-data must print exactly what a real sync sends, byte for byte.
func TestDryRunOutputMatchesTheUploadedRequestBodies(t *testing.T) {
	records := outboxRecords(t, MaxBatchBuckets+1)
	var uploaded []string
	transport := syncRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		uploaded = append(uploaded, string(body))
		var batch Batch
		if err := json.Unmarshal(body, &batch); err != nil {
			t.Fatal(err)
		}
		response := BatchResponse{}
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
	if _, err := client.Run(t.Context(), &fakeOutbox{records: records}, "device-credential"); err != nil {
		t.Fatal(err)
	}
	var preview strings.Builder
	if _, err := DryRun(t.Context(), &fakeOutbox{records: records}, &preview); err != nil {
		t.Fatal(err)
	}
	if got, want := preview.String(), strings.Join(uploaded, "\n")+"\n"; got != want {
		t.Fatalf("show-data output differs from the uploaded request bodies:\npreview: %s\nuploaded: %s", got, want)
	}
}

func TestClientReportsUpgradeRequiredWithoutRetryOrAcknowledgement(t *testing.T) {
	outbox := &fakeOutbox{records: outboxRecords(t, 1)}
	requests := 0
	transport := syncRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return syncResponse(request, http.StatusUpgradeRequired, `{"error":"upgrade_required"}`, nil), nil
	})
	client, err := NewClient("https://api.example.test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { t.Fatal("client retried a retired protocol"); return nil }
	_, err = client.Run(t.Context(), outbox, "device-credential")
	if !errors.Is(err, ErrUpgradeRequired) || !strings.Contains(err.Error(), "npx @tokenholic/cli@latest") {
		t.Fatalf("Run() error = %v, want an upgrade instruction", err)
	}
	if requests != 1 || len(outbox.acknowledged) != 0 {
		t.Fatalf("requests/acknowledged = %d/%d, want 1/0 so usage stays pending", requests, len(outbox.acknowledged))
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

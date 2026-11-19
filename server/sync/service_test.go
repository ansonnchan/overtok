package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ansonnchan/tokenholic/internal/modelcatalog"
	protocol "github.com/ansonnchan/tokenholic/internal/sync"
	serverauth "github.com/ansonnchan/tokenholic/server/auth"
)

func TestApplyUsesServerCatalogAndPricing(t *testing.T) {
	service, repository := testService(t)
	batch := pricedBatch(t)
	forgedCost := uint64(1)
	forgedRevision := "client-supplied"
	batch.Buckets[0].Bucket.APIEstimate = protocol.Value{Value: &forgedCost, State: "complete"}
	batch.Buckets[0].Bucket.PricingRevision = &forgedRevision

	response, err := service.Apply(t.Context(), "credential", batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Acknowledgements) != 1 || len(repository.buckets) != 1 {
		t.Fatalf("response/repository = %#v/%#v", response, repository.buckets)
	}
	stored := repository.buckets[0].Bucket
	if stored.APIEstimate.Value == nil || *stored.APIEstimate.Value != 14_600_000 || stored.APIEstimate.State != "complete" {
		t.Fatalf("authoritative estimate = %#v", stored.APIEstimate)
	}
	if stored.PricingRevision == nil || *stored.PricingRevision != modelcatalog.PricingRevision {
		t.Fatalf("authoritative pricing revision = %#v", stored.PricingRevision)
	}
}

func TestApplyPreservesPartialAndUnavailableEstimateState(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*protocol.Bucket)
		wantState string
		wantValue bool
	}{
		{
			name: "partial input produces lower bound", wantState: "partial", wantValue: true,
			mutate: func(bucket *protocol.Bucket) { bucket.Tokens.InputUncached.State = "partial" },
		},
		{
			name: "missing input is unavailable", wantState: "unavailable", wantValue: false,
			mutate: func(bucket *protocol.Bucket) {
				bucket.Tokens.InputUncached = protocol.Value{State: "unavailable"}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, repository := testService(t)
			batch := pricedBatch(t)
			test.mutate(&batch.Buckets[0].Bucket)
			if _, err := service.Apply(t.Context(), "credential", batch); err != nil {
				t.Fatal(err)
			}
			estimate := repository.buckets[0].Bucket.APIEstimate
			if estimate.State != test.wantState || (estimate.Value != nil) != test.wantValue {
				t.Fatalf("estimate = %#v", estimate)
			}
		})
	}
}

func TestApplyRejectsClientModelMetadataThatDisagreesWithServer(t *testing.T) {
	service, repository := testService(t)
	batch := pricedBatch(t)
	wrongProvider := "untrusted-provider"
	batch.Buckets[0].Bucket.ModelProvider = &wrongProvider
	batch.Buckets[0].BucketKey = bucketKey(batch.Buckets[0].Bucket)

	_, err := service.Apply(t.Context(), "credential", batch)
	if !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("Apply() error = %v", err)
	}
	if repository.called {
		t.Fatal("repository received an invalid batch")
	}
}

func testService(t *testing.T) (*Service, *fakeRepository) {
	t.Helper()
	models, err := modelcatalog.NewResolver(modelcatalog.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	pricing, err := modelcatalog.DefaultPricingRegistry()
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{}
	service, err := NewService(&fakeAuthenticator{}, repository, models, pricing)
	if err != nil {
		t.Fatal(err)
	}
	return service, repository
}

func pricedBatch(t *testing.T) protocol.Batch {
	t.Helper()
	million := uint64(1_000_000)
	zero := uint64(0)
	canonical, provider, pricingKey := "gpt-6.2-sol", "openai", "openai/gpt-6.2-sol"
	bucket := protocol.Bucket{
		Start: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC), Source: "codex", Surface: "cli",
		ObservedModelID: "gpt-6.2-sol", CanonicalModelID: &canonical, ModelProvider: &provider,
		PricingKey: &pricingKey, ModelResolution: "reported", Measurement: "reported", NormalizationVersion: "1",
		Tokens: protocol.Tokens{
			InputUncached:   protocol.Value{Value: &million, State: "complete"},
			InputCacheRead:  protocol.Value{Value: &million, State: "complete"},
			InputCacheWrite: protocol.Value{Value: &million, State: "complete"},
			Output:          protocol.Value{Value: &million, State: "complete"},
			ReasoningOutput: protocol.Value{Value: &zero, State: "complete"},
		},
		APIEstimate: protocol.Value{State: "unavailable"}, Revision: 1,
	}
	return protocol.Batch{SchemaVersion: protocol.SchemaVersion, Buckets: []protocol.Envelope{{
		BucketKey: bucketKey(bucket), Bucket: bucket,
	}}}
}

func bucketKey(bucket protocol.Bucket) string {
	pointerText := func(value *string) string {
		if value == nil {
			return "<null>"
		}
		return *value
	}
	parts := []string{
		bucket.Start.UTC().Format(time.RFC3339Nano), bucket.Source, bucket.Surface,
		bucket.ObservedModelID, pointerText(bucket.CanonicalModelID), pointerText(bucket.ModelProvider),
		pointerText(bucket.PricingKey), bucket.ModelResolution, bucket.Measurement, bucket.NormalizationVersion,
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

type fakeAuthenticator struct{}

func (f *fakeAuthenticator) Authenticate(context.Context, string) (serverauth.Principal, error) {
	return serverauth.Principal{UserID: "user", DeviceID: "device"}, nil
}

type fakeRepository struct {
	called  bool
	buckets []protocol.Envelope
}

func (f *fakeRepository) ApplyUsageBuckets(_ context.Context, _ serverauth.Principal, buckets []protocol.Envelope) ([]protocol.Acknowledgement, error) {
	f.called = true
	f.buckets = append([]protocol.Envelope(nil), buckets...)
	acknowledgements := make([]protocol.Acknowledgement, 0, len(buckets))
	for _, envelope := range buckets {
		acknowledgements = append(acknowledgements, protocol.Acknowledgement{
			BucketKey: envelope.BucketKey, Revision: envelope.Bucket.Revision, Status: protocol.AckApplied,
		})
	}
	return acknowledgements, nil
}

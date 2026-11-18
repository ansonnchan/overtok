package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	protocol "github.com/ansonnchan/overtok/internal/sync"
	serveraccount "github.com/ansonnchan/overtok/server/account"
	serverauth "github.com/ansonnchan/overtok/server/auth"
)

func TestAuthenticationRoutesAndStableErrors(t *testing.T) {
	service := &fakeAuthService{
		start: serverauth.StartResponse{
			FlowToken: "sealed", UserCode: "ABCD", VerificationURI: "https://github.com/login/device",
			ExpiresIn: 15 * time.Minute, Interval: 5 * time.Second,
		},
		poll:    serverauth.PollResponse{Status: serverauth.FlowPending, RetryAfter: 7 * time.Second},
		refresh: serverauth.Session{Credential: "replacement", DeviceID: "device", UserID: "user"},
	}
	handler, err := New(service, &fakeUsageSyncService{}, &fakeAccountUsageService{})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		path       string
		body       string
		credential string
		wantStatus int
		wantBody   string
	}{
		{name: "start", path: "/v1/auth/device/start", body: `{"client_version":"test"}`, wantStatus: http.StatusCreated, wantBody: `"flow_token":"sealed"`},
		{name: "unknown field", path: "/v1/auth/device/start", body: `{"private_path":"no"}`, wantStatus: http.StatusBadRequest, wantBody: "invalid_request"},
		{name: "pending", path: "/v1/auth/device/poll", body: `{"flow_token":"sealed"}`, wantStatus: http.StatusAccepted, wantBody: "pending"},
		{name: "refresh", path: "/v1/auth/refresh", credential: "old", wantStatus: http.StatusOK, wantBody: "replacement"},
		{name: "refresh missing bearer", path: "/v1/auth/refresh", wantStatus: http.StatusUnauthorized, wantBody: "unauthorized"},
		{name: "logout", path: "/v1/auth/logout", credential: "old", wantStatus: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			if test.credential != "" {
				request.Header.Set("Authorization", "Bearer "+test.credential)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache header = %q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestHandlerRedactsServiceFailures(t *testing.T) {
	service := &fakeAuthService{startErr: errors.New("provider token and private/path")}
	handler, err := New(service, &fakeUsageSyncService{}, &fakeAccountUsageService{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/device/start", bytes.NewBufferString(`{"client_version":"test"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "provider token") || strings.Contains(response.Body.String(), "private/path") {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestSyncRouteAuthenticatesAndReturnsExactAcknowledgement(t *testing.T) {
	service := &fakeAuthService{}
	syncService := &fakeUsageSyncService{}
	handler, err := New(service, syncService, &fakeAccountUsageService{})
	if err != nil {
		t.Fatal(err)
	}
	batch := validSyncBatch()
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/sync/buckets", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer device-credential")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || syncService.credential != "device-credential" || !strings.Contains(response.Body.String(), batch.Buckets[0].BucketKey) {
		t.Fatalf("sync response/service = %d %q/%#v", response.Code, response.Body.String(), syncService)
	}
}

func TestAccountUsageRouteRequiresCredential(t *testing.T) {
	accountService := &fakeAccountUsageService{usage: serveraccount.Usage{
		UserID: "user", DisplayName: "synthetic-user", GeneratedAt: time.Now().UTC(),
	}}
	handler, err := New(&fakeAuthService{}, &fakeUsageSyncService{}, accountService)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/account/usage", nil)
	request.Header.Set("Authorization", "Bearer device-credential")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || accountService.credential != "device-credential" || !strings.Contains(response.Body.String(), "synthetic-user") {
		t.Fatalf("account response = %d %q/%#v", response.Code, response.Body.String(), accountService)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/account/usage", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized account response = %d %q", response.Code, response.Body.String())
	}
}

func validSyncBatch() protocol.Batch {
	zero, output := uint64(0), uint64(7)
	bucket := protocol.Bucket{
		Start:  time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
		Source: "codex", Surface: "cli", ObservedModelID: "unknown-model",
		ModelResolution: "unknown", Measurement: "reported", NormalizationVersion: "1",
		Tokens: protocol.Tokens{
			InputUncached:   protocol.Value{Value: &zero, State: "complete"},
			InputCacheRead:  protocol.Value{State: "unavailable"},
			InputCacheWrite: protocol.Value{State: "unavailable"},
			Output:          protocol.Value{Value: &output, State: "complete"},
			ReasoningOutput: protocol.Value{State: "unavailable"},
		},
		APIEstimate: protocol.Value{State: "unavailable"}, Revision: 1,
	}
	parts := []string{
		bucket.Start.Format(time.RFC3339Nano), bucket.Source, bucket.Surface,
		bucket.ObservedModelID, "<null>", "<null>", "<null>",
		bucket.ModelResolution, bucket.Measurement, bucket.NormalizationVersion,
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return protocol.Batch{SchemaVersion: protocol.SchemaVersion, Buckets: []protocol.Envelope{{
		BucketKey: hex.EncodeToString(digest[:]), Bucket: bucket,
	}}}
}

type fakeAuthService struct {
	start     serverauth.StartResponse
	startErr  error
	poll      serverauth.PollResponse
	refresh   serverauth.Session
	logoutErr error
}

func (f *fakeAuthService) Start(context.Context, string) (serverauth.StartResponse, error) {
	return f.start, f.startErr
}
func (f *fakeAuthService) Poll(context.Context, string) (serverauth.PollResponse, error) {
	return f.poll, nil
}
func (f *fakeAuthService) Refresh(context.Context, string) (serverauth.Session, error) {
	return f.refresh, nil
}
func (f *fakeAuthService) Logout(context.Context, string) error { return f.logoutErr }

type fakeUsageSyncService struct {
	credential string
}

type fakeAccountUsageService struct {
	credential string
	usage      serveraccount.Usage
}

func (f *fakeAccountUsageService) Usage(_ context.Context, credential string) (serveraccount.Usage, error) {
	f.credential = credential
	return f.usage, nil
}

func (f *fakeUsageSyncService) Apply(_ context.Context, credential string, batch protocol.Batch) (protocol.BatchResponse, error) {
	f.credential = credential
	acknowledgements := make([]protocol.Acknowledgement, 0, len(batch.Buckets))
	for _, bucket := range batch.Buckets {
		acknowledgements = append(acknowledgements, protocol.Acknowledgement{
			BucketKey: bucket.BucketKey, Revision: bucket.Bucket.Revision, Status: protocol.AckApplied,
		})
	}
	return protocol.BatchResponse{Acknowledgements: acknowledgements}, nil
}

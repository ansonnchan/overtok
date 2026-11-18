package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	handler, err := New(service)
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
	handler, err := New(service)
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

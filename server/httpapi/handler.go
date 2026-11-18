package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	protocol "github.com/ansonnchan/overtok/internal/sync"
	serverauth "github.com/ansonnchan/overtok/server/auth"
	serversync "github.com/ansonnchan/overtok/server/sync"
)

const (
	maxRequestBytes     = 16 << 10
	maxSyncRequestBytes = 1 << 20
)

type authenticationService interface {
	Start(context.Context, string) (serverauth.StartResponse, error)
	Poll(context.Context, string) (serverauth.PollResponse, error)
	Refresh(context.Context, string) (serverauth.Session, error)
	Logout(context.Context, string) error
}

type usageSyncService interface {
	Apply(context.Context, string, protocol.Batch) (protocol.BatchResponse, error)
}

// Handler is the hosted Overtok HTTP API.
type Handler struct {
	auth authenticationService
	sync usageSyncService
	mux  *http.ServeMux
}

// New constructs the API handler with exact method-aware routes.
func New(authService authenticationService, syncService usageSyncService) (*Handler, error) {
	if authService == nil || syncService == nil {
		return nil, errors.New("initialize HTTP API: authentication and synchronization services are required")
	}
	handler := &Handler{auth: authService, sync: syncService, mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET /healthz", handler.health)
	handler.mux.HandleFunc("POST /v1/auth/device/start", handler.startDevice)
	handler.mux.HandleFunc("POST /v1/auth/device/poll", handler.pollDevice)
	handler.mux.HandleFunc("POST /v1/auth/refresh", handler.refresh)
	handler.mux.HandleFunc("POST /v1/auth/logout", handler.logout)
	handler.mux.HandleFunc("POST /v1/sync/buckets", handler.syncBuckets)
	return handler, nil
}

func (h *Handler) syncBuckets(response http.ResponseWriter, request *http.Request) {
	credential, ok := bearerCredential(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "unauthorized")
		return
	}
	var batch protocol.Batch
	if err := decodeRequestLimit(response, request, &batch, maxSyncRequestBytes); err != nil || batch.Validate() != nil {
		writeError(response, http.StatusBadRequest, "invalid_batch")
		return
	}
	result, err := h.sync.Apply(request.Context(), credential, batch)
	switch {
	case errors.Is(err, serverauth.ErrUnauthorized):
		writeError(response, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, serversync.ErrRevisionConflict):
		writeError(response, http.StatusConflict, "revision_conflict")
	case err != nil:
		writeError(response, http.StatusInternalServerError, "internal_error")
	default:
		writeJSON(response, http.StatusOK, result)
	}
}

// ServeHTTP applies stable security headers before routing.
func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(response, request)
}

func (h *Handler) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) startDevice(response http.ResponseWriter, request *http.Request) {
	var input struct {
		ClientVersion string `json:"client_version"`
	}
	if err := decodeRequest(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	started, err := h.auth.Start(request.Context(), input.ClientVersion)
	if err != nil {
		writeError(response, http.StatusBadGateway, "authorization_unavailable")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"flow_token": started.FlowToken, "user_code": started.UserCode,
		"verification_uri": started.VerificationURI,
		"expires_in":       durationSeconds(started.ExpiresIn),
		"interval":         durationSeconds(started.Interval),
	})
}

func (h *Handler) pollDevice(response http.ResponseWriter, request *http.Request) {
	var input struct {
		FlowToken string `json:"flow_token"`
	}
	if err := decodeRequest(response, request, &input); err != nil || input.FlowToken == "" {
		writeError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.auth.Poll(request.Context(), input.FlowToken)
	if err != nil {
		writeError(response, http.StatusBadGateway, "authorization_unavailable")
		return
	}
	switch result.Status {
	case serverauth.FlowPending, serverauth.FlowSlowDown:
		retry := result.RetryAfter
		if retry <= 0 {
			retry = 5 * time.Second
		}
		response.Header().Set("Retry-After", strconv.FormatInt(durationSeconds(retry), 10))
		writeJSON(response, http.StatusAccepted, map[string]any{
			"status": result.Status, "retry_after": durationSeconds(retry),
		})
	case serverauth.FlowExpired:
		writeError(response, http.StatusGone, "authorization_expired")
	case serverauth.FlowDenied:
		writeError(response, http.StatusForbidden, "authorization_denied")
	case serverauth.FlowApproved:
		if result.Session == nil {
			writeError(response, http.StatusInternalServerError, "internal_error")
			return
		}
		writeJSON(response, http.StatusOK, result.Session)
	default:
		writeError(response, http.StatusBadGateway, "authorization_unavailable")
	}
}

func (h *Handler) refresh(response http.ResponseWriter, request *http.Request) {
	credential, ok := bearerCredential(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "unauthorized")
		return
	}
	session, err := h.auth.Refresh(request.Context(), credential)
	if errors.Is(err, serverauth.ErrUnauthorized) {
		writeError(response, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(response, http.StatusOK, session)
}

func (h *Handler) logout(response http.ResponseWriter, request *http.Request) {
	credential, ok := bearerCredential(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.auth.Logout(request.Context(), credential); errors.Is(err, serverauth.ErrUnauthorized) {
		writeError(response, http.StatusUnauthorized, "unauthorized")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func decodeRequest(response http.ResponseWriter, request *http.Request, target any) error {
	return decodeRequestLimit(response, request, target, maxRequestBytes)
}

func decodeRequestLimit(response http.ResponseWriter, request *http.Request, target any, maximum int64) error {
	request.Body = http.MaxBytesReader(response, request.Body, maximum)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func bearerCredential(request *http.Request) (string, bool) {
	scheme, value, found := strings.Cut(strings.TrimSpace(request.Header.Get("Authorization")), " ")
	return value, found && strings.EqualFold(scheme, "Bearer") && value != ""
}

func durationSeconds(duration time.Duration) int64 {
	seconds := int64(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func writeError(response http.ResponseWriter, status int, code string) {
	writeJSON(response, status, map[string]string{"error": code})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

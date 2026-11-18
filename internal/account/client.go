package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

// ErrUnauthorized means the stored Overtok device credential is no longer valid.
var ErrUnauthorized = errors.New("stored device credential is unauthorized")

// Session is the authentication state stored in the OS credential vault.
type Session struct {
	Credential string    `json:"credential"`
	UserID     string    `json:"user_id"`
	DeviceID   string    `json:"device_id"`
	Login      string    `json:"login"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// DeviceStart is a CLI-friendly GitHub device authorization challenge.
type DeviceStart struct {
	FlowToken       string
	UserCode        string
	VerificationURI string
	ExpiresIn       time.Duration
	Interval        time.Duration
}

// PollResult is one device authorization status.
type PollResult struct {
	Pending    bool
	RetryAfter time.Duration
	Session    Session
}

// Client calls the hosted authentication API.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// NewClient validates the API origin. Plain HTTP is accepted only for a
// loopback development server.
func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid Overtok API URL")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return nil, errors.New("Overtok API URL must use HTTPS or loopback HTTP")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: parsed, httpClient: &clientCopy}, nil
}

// Start begins GitHub device authorization through Overtok.
func (c *Client) Start(ctx context.Context, clientVersion string) (DeviceStart, error) {
	var response struct {
		FlowToken       string `json:"flow_token"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int64  `json:"expires_in"`
		Interval        int64  `json:"interval"`
	}
	if err := c.request(ctx, http.MethodPost, "/v1/auth/device/start", "", map[string]string{"client_version": clientVersion}, &response); err != nil {
		return DeviceStart{}, err
	}
	if response.FlowToken == "" || response.UserCode == "" || response.VerificationURI == "" || response.ExpiresIn <= 0 || response.Interval <= 0 {
		return DeviceStart{}, errors.New("authorization server returned an incomplete device challenge")
	}
	return DeviceStart{
		FlowToken: response.FlowToken, UserCode: response.UserCode,
		VerificationURI: response.VerificationURI,
		ExpiresIn:       time.Duration(response.ExpiresIn) * time.Second,
		Interval:        time.Duration(response.Interval) * time.Second,
	}, nil
}

// Poll checks the current device authorization status.
func (c *Client) Poll(ctx context.Context, flowToken string) (PollResult, error) {
	var session Session
	status, header, err := c.do(ctx, http.MethodPost, "/v1/auth/device/poll", "", map[string]string{"flow_token": flowToken}, &session)
	if err != nil {
		return PollResult{}, err
	}
	switch status {
	case http.StatusOK:
		if session.Credential == "" || session.UserID == "" || session.DeviceID == "" || session.ExpiresAt.IsZero() {
			return PollResult{}, errors.New("authorization server returned an incomplete session")
		}
		return PollResult{Session: session}, nil
	case http.StatusAccepted:
		retry := parseRetryAfter(header.Get("Retry-After"))
		if retry <= 0 {
			retry = 5 * time.Second
		}
		return PollResult{Pending: true, RetryAfter: retry}, nil
	case http.StatusGone:
		return PollResult{}, errors.New("GitHub device authorization expired")
	case http.StatusForbidden:
		return PollResult{}, errors.New("GitHub device authorization was denied")
	default:
		return PollResult{}, fmt.Errorf("authorization server returned status %d", status)
	}
}

// Refresh rotates the current Overtok device credential.
func (c *Client) Refresh(ctx context.Context, credential string) (Session, error) {
	var session Session
	if err := c.request(ctx, http.MethodPost, "/v1/auth/refresh", credential, nil, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

// Logout revokes the current Overtok device.
func (c *Client) Logout(ctx context.Context, credential string) error {
	status, _, err := c.do(ctx, http.MethodPost, "/v1/auth/logout", credential, nil, nil)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if status != http.StatusNoContent {
		return fmt.Errorf("authorization server returned status %d", status)
	}
	return nil
}

func (c *Client) request(ctx context.Context, method, path, credential string, input, output any) error {
	status, _, err := c.do(ctx, method, path, credential, input, output)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("authorization server returned status %d", status)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path, credential string, input, output any) (int, http.Header, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, nil, fmt.Errorf("encode API request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	endpoint := *c.baseURL
	endpoint.Path += path
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return 0, nil, fmt.Errorf("create API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("request Overtok API: %w", err)
	}
	defer response.Body.Close()
	if output != nil && response.StatusCode >= 200 && response.StatusCode < 300 && response.StatusCode != http.StatusNoContent {
		decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
		if err := decoder.Decode(output); err != nil {
			return 0, nil, fmt.Errorf("decode Overtok API response: %w", err)
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	}
	return response.StatusCode, response.Header.Clone(), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func parseRetryAfter(value string) time.Duration {
	duration, err := time.ParseDuration(value + "s")
	if err != nil || duration <= 0 {
		return 0
	}
	return duration
}

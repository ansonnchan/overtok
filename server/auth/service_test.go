package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceDeviceFlowRegistersOnlyTokenholicCredential(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	github := &fakeGitHub{
		authorization: DeviceAuthorization{
			DeviceCode: "github-device-secret", UserCode: "ABCD-EFGH",
			VerificationURI: "https://github.com/login/device",
			ExpiresAt:       now.Add(15 * time.Minute), Interval: 5 * time.Second,
		},
		poll: GitHubPollResult{Status: FlowApproved, AccessToken: "github-access-secret"},
		user: GitHubUser{ID: 101, Login: "synthetic-user", Name: "Ignored Name"},
	}
	repository := &fakeRepository{}
	service, err := NewService(github, repository, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	started, err := service.Start(t.Context(), "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if started.FlowToken == "" || strings.Contains(started.FlowToken, "github-device-secret") {
		t.Fatalf("flow token exposes device code: %q", started.FlowToken)
	}
	result, err := service.Poll(t.Context(), started.FlowToken)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != FlowApproved || result.Session == nil || result.Session.Credential == "" || result.Session.Login != "synthetic-user" {
		t.Fatalf("Poll() = %#v", result)
	}
	if github.userToken != "github-access-secret" || repository.registered.GitHub.ID != 101 {
		t.Fatalf("GitHub/repository exchange = %#v/%#v", github, repository.registered)
	}
	if repository.registered.Device.ClientVersion != "test-version" || repository.registered.Device.Credential.SecretHash == [32]byte{} {
		t.Fatalf("registration = %#v", repository.registered)
	}
	if !validUUID(repository.registered.Device.UserID) || !validUUID(repository.registered.Device.DeviceID) {
		t.Fatalf("generated identifiers = %q/%q", repository.registered.Device.UserID, repository.registered.Device.DeviceID)
	}
	if _, err := parseCredential(result.Session.Credential); err != nil {
		t.Fatalf("generated credential does not parse: %v", err)
	}
	if strings.Contains(result.Session.Credential, "github") {
		t.Fatalf("session contains provider credential: %q", result.Session.Credential)
	}
	repeated, err := service.Poll(t.Context(), started.FlowToken)
	if err != nil || repeated.Session == nil || repeated.Session.Credential != result.Session.Credential || repeated.Session.DeviceID != result.Session.DeviceID {
		t.Fatalf("repeated Poll() = %#v, %v", repeated, err)
	}
}

func TestServiceRejectsTamperedExpiredAndDeniedFlows(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	github := &fakeGitHub{authorization: DeviceAuthorization{
		DeviceCode: "code", UserCode: "CODE", VerificationURI: "https://github.com/login/device",
		ExpiresAt: now.Add(time.Minute), Interval: time.Second,
	}}
	service, err := NewService(github, &fakeRepository{}, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	started, err := service.Start(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	tampered := []byte(started.FlowToken)
	if tampered[len(tampered)/2] == 'A' {
		tampered[len(tampered)/2] = 'B'
	} else {
		tampered[len(tampered)/2] = 'A'
	}
	for _, token := range []string{"invalid", string(tampered)} {
		result, err := service.Poll(t.Context(), token)
		if err != nil || result.Status != FlowExpired {
			t.Fatalf("tampered Poll() = %#v, %v", result, err)
		}
	}
	service.now = func() time.Time { return now.Add(2 * time.Minute) }
	result, err := service.Poll(t.Context(), started.FlowToken)
	if err != nil || result.Status != FlowExpired {
		t.Fatalf("expired Poll() = %#v, %v", result, err)
	}
	service.now = func() time.Time { return now }
	github.poll = GitHubPollResult{Status: FlowDenied}
	result, err = service.Poll(t.Context(), started.FlowToken)
	if err != nil || result.Status != FlowDenied {
		t.Fatalf("denied Poll() = %#v, %v", result, err)
	}
}

func TestServiceValidatesAndNormalizesClientVersion(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	github := &fakeGitHub{authorization: DeviceAuthorization{
		DeviceCode: "code", UserCode: "CODE", VerificationURI: "https://github.com/login/device",
		ExpiresAt: now.Add(time.Minute), Interval: time.Second,
	}}
	service, err := NewService(github, &fakeRepository{}, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	for _, version := range []string{"", " \t", "invalid\nversion", strings.Repeat("v", 65)} {
		if _, err := service.Start(t.Context(), version); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("Start(%q) error = %v", version, err)
		}
	}
	started, err := service.Start(t.Context(), "  test-version  ")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := service.states.open(started.FlowToken, now)
	if err != nil || claims.ClientVersion != "test-version" {
		t.Fatalf("client version claims = %#v, %v", claims, err)
	}
}

func TestServiceRefreshFailureAndRevocation(t *testing.T) {
	repository := &fakeRepository{principal: Principal{
		UserID:   "00000000-0000-4000-8000-000000000001",
		DeviceID: "00000000-0000-4000-8000-000000000002",
	}}
	service, err := NewService(&fakeGitHub{}, repository, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }
	credential, _, err := generateCredential(service.now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_, plaintext, err := generateCredential(service.now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	repository.rotateErr = ErrUnauthorized
	if _, err := service.Refresh(t.Context(), plaintext); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Refresh() error = %v", err)
	}
	repository.rotateErr = nil
	session, err := service.Refresh(t.Context(), plaintext)
	if err != nil || session.Credential == plaintext || session.DeviceID != repository.principal.DeviceID {
		t.Fatalf("Refresh() = %#v, %v", session, err)
	}
	if repository.rotated.CredentialID == credential.CredentialID {
		t.Fatal("rotation reused a credential identifier")
	}
	if err := service.Logout(t.Context(), plaintext); err != nil || !repository.revoked {
		t.Fatalf("Logout() = %v, revoked=%v", err, repository.revoked)
	}
	if _, err := service.Refresh(t.Context(), "malformed"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("malformed Refresh() error = %v", err)
	}
}

type fakeGitHub struct {
	authorization DeviceAuthorization
	poll          GitHubPollResult
	user          GitHubUser
	userToken     string
}

func (f *fakeGitHub) Start(context.Context) (DeviceAuthorization, error)     { return f.authorization, nil }
func (f *fakeGitHub) Poll(context.Context, string) (GitHubPollResult, error) { return f.poll, nil }
func (f *fakeGitHub) User(_ context.Context, token string) (GitHubUser, error) {
	f.userToken = token
	return f.user, nil
}

type fakeRepository struct {
	registered struct {
		GitHub GitHubUser
		Device DeviceRegistration
	}
	principal Principal
	rotated   NewCredential
	rotateErr error
	revoked   bool
}

func (f *fakeRepository) RegisterDevice(_ context.Context, user GitHubUser, device DeviceRegistration) (Identity, error) {
	f.registered.GitHub, f.registered.Device = user, device
	return Identity{UserID: device.UserID, DeviceID: device.DeviceID, Login: user.Login}, nil
}
func (f *fakeRepository) Authenticate(context.Context, CredentialProof) (Principal, error) {
	return f.principal, nil
}
func (f *fakeRepository) RotateCredential(_ context.Context, _ CredentialProof, credential NewCredential) (Principal, error) {
	f.rotated = credential
	return f.principal, f.rotateErr
}
func (f *fakeRepository) RevokeDevice(context.Context, CredentialProof) error {
	f.revoked = true
	return nil
}

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultCredentialLifetime = 90 * 24 * time.Hour

// Service coordinates GitHub device flow and Tokenholic device credentials.
type Service struct {
	github             GitHub
	repository         Repository
	states             *stateSealer
	now                func() time.Time
	credentialLifetime time.Duration
}

// NewService constructs the authentication boundary. stateKey must be a
// dedicated 32-byte secret used only for short-lived device-flow state.
func NewService(github GitHub, repository Repository, stateKey []byte) (*Service, error) {
	if github == nil || repository == nil {
		return nil, errors.New("initialize authentication: GitHub client and repository are required")
	}
	states, err := newStateSealer(stateKey)
	if err != nil {
		return nil, fmt.Errorf("initialize authentication: %w", err)
	}
	return &Service{
		github: github, repository: repository, states: states,
		now: time.Now, credentialLifetime: defaultCredentialLifetime,
	}, nil
}

// Start begins a GitHub device authorization without accepting a password.
func (s *Service) Start(ctx context.Context, clientVersion string) (StartResponse, error) {
	if len(clientVersion) > 64 {
		return StartResponse{}, errors.New("client version is too long")
	}
	authorization, err := s.github.Start(ctx)
	if err != nil {
		return StartResponse{}, fmt.Errorf("start GitHub device authorization: %w", err)
	}
	now := s.now().UTC()
	if authorization.DeviceCode == "" || authorization.UserCode == "" || authorization.VerificationURI == "" || !authorization.ExpiresAt.After(now) {
		return StartResponse{}, errors.New("start GitHub device authorization: incomplete response")
	}
	interval := authorization.Interval
	if interval < time.Second {
		interval = 5 * time.Second
	}
	credential, plaintext, err := generateCredential(now.Add(s.credentialLifetime))
	if err != nil {
		return StartResponse{}, err
	}
	deviceID, err := randomUUID()
	if err != nil {
		return StartResponse{}, err
	}
	userID, err := randomUUID()
	if err != nil {
		return StartResponse{}, err
	}
	flowToken, err := s.states.seal(flowClaims{
		DeviceCode: authorization.DeviceCode, ClientVersion: clientVersion,
		UserID: userID, DeviceID: deviceID, Credential: plaintext,
		CredentialExpiresAt: credential.ExpiresAt.Unix(), ExpiresAt: authorization.ExpiresAt.Unix(),
	})
	if err != nil {
		return StartResponse{}, err
	}
	return StartResponse{
		FlowToken: flowToken, UserCode: authorization.UserCode,
		VerificationURI: authorization.VerificationURI,
		ExpiresIn:       authorization.ExpiresAt.Sub(now), Interval: interval,
	}, nil
}

// Poll checks a device authorization and registers a device after approval.
func (s *Service) Poll(ctx context.Context, flowToken string) (PollResponse, error) {
	claims, err := s.states.open(flowToken, s.now().UTC())
	if err != nil {
		return PollResponse{Status: FlowExpired}, nil
	}
	result, err := s.github.Poll(ctx, claims.DeviceCode)
	if err != nil {
		return PollResponse{}, fmt.Errorf("poll GitHub device authorization: %w", err)
	}
	if result.Status != FlowApproved {
		return PollResponse{Status: result.Status, RetryAfter: result.RetryAfter}, nil
	}
	if result.AccessToken == "" {
		return PollResponse{}, errors.New("poll GitHub device authorization: approved response has no token")
	}
	githubUser, err := s.github.User(ctx, result.AccessToken)
	result.AccessToken = ""
	if err != nil {
		return PollResponse{}, fmt.Errorf("resolve GitHub identity: %w", err)
	}
	if githubUser.ID <= 0 || !validGitHubLogin(githubUser.Login) {
		return PollResponse{}, errors.New("resolve GitHub identity: invalid stable identity")
	}
	proof, err := parseCredential(claims.Credential)
	if err != nil {
		return PollResponse{}, errors.New("poll GitHub device authorization: invalid sealed credential")
	}
	credential := NewCredential{
		CredentialID: proof.CredentialID, SecretHash: proof.SecretHash,
		ExpiresAt: time.Unix(claims.CredentialExpiresAt, 0).UTC(),
	}
	identity, err := s.repository.RegisterDevice(ctx, githubUser, DeviceRegistration{
		UserID: claims.UserID, DeviceID: claims.DeviceID, ClientVersion: claims.ClientVersion, Credential: credential,
	})
	if err != nil {
		return PollResponse{}, fmt.Errorf("register authenticated device: %w", err)
	}
	return PollResponse{Status: FlowApproved, Session: &Session{
		Credential: claims.Credential, UserID: identity.UserID, DeviceID: identity.DeviceID,
		Login: identity.Login, ExpiresAt: credential.ExpiresAt,
	}}, nil
}

// Authenticate validates a Tokenholic device credential.
func (s *Service) Authenticate(ctx context.Context, credential string) (Principal, error) {
	proof, err := parseCredential(credential)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	return s.repository.Authenticate(ctx, proof)
}

// Refresh rotates a valid device credential and revokes its previous value.
func (s *Service) Refresh(ctx context.Context, credential string) (Session, error) {
	proof, err := parseCredential(credential)
	if err != nil {
		return Session{}, ErrUnauthorized
	}
	replacement, plaintext, err := generateCredential(s.now().UTC().Add(s.credentialLifetime))
	if err != nil {
		return Session{}, err
	}
	principal, err := s.repository.RotateCredential(ctx, proof, replacement)
	if err != nil {
		return Session{}, err
	}
	return Session{
		Credential: plaintext, UserID: principal.UserID, DeviceID: principal.DeviceID,
		ExpiresAt: replacement.ExpiresAt,
	}, nil
}

// Logout revokes the authenticated device and all of its credentials.
func (s *Service) Logout(ctx context.Context, credential string) error {
	proof, err := parseCredential(credential)
	if err != nil {
		return ErrUnauthorized
	}
	return s.repository.RevokeDevice(ctx, proof)
}

func generateCredential(expiresAt time.Time) (NewCredential, string, error) {
	id, err := randomUUID()
	if err != nil {
		return NewCredential{}, "", err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return NewCredential{}, "", fmt.Errorf("generate device credential: %w", err)
	}
	hash := sha256.Sum256(secret)
	plaintext := id + "." + base64.RawURLEncoding.EncodeToString(secret)
	return NewCredential{CredentialID: id, SecretHash: hash, ExpiresAt: expiresAt}, plaintext, nil
}

func parseCredential(credential string) (CredentialProof, error) {
	id, encoded, found := strings.Cut(credential, ".")
	if !found || !validUUID(id) {
		return CredentialProof{}, ErrUnauthorized
	}
	secret, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(secret) != 32 {
		return CredentialProof{}, ErrUnauthorized
	}
	return CredentialProof{CredentialID: id, SecretHash: sha256.Sum256(secret)}, nil
}

func randomUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func validGitHubLogin(login string) bool {
	if len(login) < 1 || len(login) > 39 || login[0] == '-' || login[len(login)-1] == '-' || strings.Contains(login, "--") {
		return false
	}
	for _, character := range login {
		if !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
			return false
		}
	}
	return true
}

package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const stateAdditionalData = "overtok-device-flow-v1"

type flowClaims struct {
	DeviceCode          string `json:"device_code"`
	ClientVersion       string `json:"client_version"`
	UserID              string `json:"user_id"`
	DeviceID            string `json:"device_id"`
	Credential          string `json:"credential"`
	CredentialExpiresAt int64  `json:"credential_expires_at"`
	ExpiresAt           int64  `json:"expires_at"`
}

type stateSealer struct {
	aead cipher.AEAD
}

func newStateSealer(key []byte) (*stateSealer, error) {
	if len(key) != 32 {
		return nil, errors.New("device flow state key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize device flow state cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize device flow state sealing: %w", err)
	}
	return &stateSealer{aead: aead}, nil
}

func (s *stateSealer) seal(claims flowClaims) (string, error) {
	plaintext, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode device flow state: %w", err)
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate device flow state nonce: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, plaintext, []byte(stateAdditionalData))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *stateSealer) open(token string, now time.Time) (flowClaims, error) {
	encoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(encoded) <= s.aead.NonceSize() {
		return flowClaims{}, errors.New("invalid device flow state")
	}
	nonce, ciphertext := encoded[:s.aead.NonceSize()], encoded[s.aead.NonceSize():]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, []byte(stateAdditionalData))
	if err != nil {
		return flowClaims{}, errors.New("invalid device flow state")
	}
	var claims flowClaims
	if err := json.Unmarshal(plaintext, &claims); err != nil || claims.DeviceCode == "" ||
		!validUUID(claims.UserID) || !validUUID(claims.DeviceID) || claims.Credential == "" ||
		claims.CredentialExpiresAt <= 0 || claims.ExpiresAt <= 0 {
		return flowClaims{}, errors.New("invalid device flow state")
	}
	if !now.Before(time.Unix(claims.ExpiresAt, 0)) {
		return flowClaims{}, errors.New("device flow state expired")
	}
	return claims, nil
}

package postgres

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	serverauth "github.com/ansonnchan/overtok/server/auth"
)

func TestPostgresAuthenticationLifecycle(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	repository, err := NewAuthRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	firstHash := sha256.Sum256([]byte("synthetic-first-secret"))
	secondHash := sha256.Sum256([]byte("synthetic-second-secret"))
	rotatedHash := sha256.Sum256([]byte("synthetic-rotated-secret"))
	expires := time.Now().UTC().Add(time.Hour)
	first, err := repository.RegisterDevice(ctx, serverauth.GitHubUser{ID: 501, Login: "synthetic-user"}, serverauth.DeviceRegistration{
		UserID: "10000000-0000-4000-8000-000000000001", DeviceID: "10000000-0000-4000-8000-000000000011", ClientVersion: "test",
		Credential: serverauth.NewCredential{CredentialID: "10000000-0000-4000-8000-000000000021", SecretHash: firstHash, ExpiresAt: expires},
	})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := repository.RegisterDevice(ctx, serverauth.GitHubUser{ID: 501, Login: "synthetic-user"}, serverauth.DeviceRegistration{
		UserID: "10000000-0000-4000-8000-000000000001", DeviceID: "10000000-0000-4000-8000-000000000011", ClientVersion: "test",
		Credential: serverauth.NewCredential{CredentialID: "10000000-0000-4000-8000-000000000021", SecretHash: firstHash, ExpiresAt: expires},
	})
	if err != nil || repeated.UserID != first.UserID || repeated.DeviceID != first.DeviceID {
		t.Fatalf("repeated RegisterDevice() = %#v, %v", repeated, err)
	}
	second, err := repository.RegisterDevice(ctx, serverauth.GitHubUser{ID: 501, Login: "renamed-user"}, serverauth.DeviceRegistration{
		UserID: "10000000-0000-4000-8000-000000000002", DeviceID: "10000000-0000-4000-8000-000000000012", ClientVersion: "test",
		Credential: serverauth.NewCredential{CredentialID: "10000000-0000-4000-8000-000000000022", SecretHash: secondHash, ExpiresAt: expires},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.UserID != second.UserID || first.DeviceID == second.DeviceID || second.Login != "renamed-user" {
		t.Fatalf("device identities = %#v/%#v", first, second)
	}
	firstProof := serverauth.CredentialProof{CredentialID: "10000000-0000-4000-8000-000000000021", SecretHash: firstHash}
	principal, err := repository.Authenticate(ctx, firstProof)
	if err != nil || principal.UserID != first.UserID || principal.DeviceID != first.DeviceID {
		t.Fatalf("Authenticate() = %#v, %v", principal, err)
	}
	wrong := firstProof
	wrong.SecretHash = secondHash
	if _, err := repository.Authenticate(ctx, wrong); !errors.Is(err, serverauth.ErrUnauthorized) {
		t.Fatalf("wrong secret error = %v", err)
	}
	rotated, err := repository.RotateCredential(ctx, firstProof, serverauth.NewCredential{
		CredentialID: "10000000-0000-4000-8000-000000000023", SecretHash: rotatedHash, ExpiresAt: expires,
	})
	if err != nil || rotated.DeviceID != first.DeviceID {
		t.Fatalf("RotateCredential() = %#v, %v", rotated, err)
	}
	if _, err := repository.Authenticate(ctx, firstProof); !errors.Is(err, serverauth.ErrUnauthorized) {
		t.Fatalf("old credential error = %v", err)
	}
	rotatedProof := serverauth.CredentialProof{CredentialID: rotated.CredentialID, SecretHash: rotatedHash}
	if err := repository.RevokeDevice(ctx, rotatedProof); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Authenticate(ctx, rotatedProof); !errors.Is(err, serverauth.ErrUnauthorized) {
		t.Fatalf("revoked credential error = %v", err)
	}
	secondProof := serverauth.CredentialProof{CredentialID: "10000000-0000-4000-8000-000000000022", SecretHash: secondHash}
	if _, err := repository.Authenticate(ctx, secondProof); err != nil {
		t.Fatalf("other device was revoked: %v", err)
	}
}

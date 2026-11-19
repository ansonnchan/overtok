package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	serverauth "github.com/ansonnchan/tokenholic/server/auth"
	servergroup "github.com/ansonnchan/tokenholic/server/group"
	"github.com/jackc/pgx/v5"
)

func TestPostgresAccountDeletionErasesOwnedStateOnly(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	authRepository, err := NewAuthRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	ownerHash := sha256.Sum256([]byte("deletion-owner-secret"))
	ownerSecondHash := sha256.Sum256([]byte("deletion-owner-second-secret"))
	otherHash := sha256.Sum256([]byte("deletion-other-secret"))
	owner, err := authRepository.RegisterDevice(ctx, serverauth.GitHubUser{ID: 801, Login: "delete-owner"}, serverauth.DeviceRegistration{
		UserID: "80000000-0000-4000-8000-000000000001", DeviceID: "80000000-0000-4000-8000-000000000011",
		Credential: serverauth.NewCredential{CredentialID: "80000000-0000-4000-8000-000000000021", SecretHash: ownerHash, ExpiresAt: expires},
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerSecond, err := authRepository.RegisterDevice(ctx, serverauth.GitHubUser{ID: 801, Login: "delete-owner"}, serverauth.DeviceRegistration{
		UserID: "80000000-0000-4000-8000-000000000002", DeviceID: "80000000-0000-4000-8000-000000000012",
		Credential: serverauth.NewCredential{CredentialID: "80000000-0000-4000-8000-000000000022", SecretHash: ownerSecondHash, ExpiresAt: expires},
	})
	if err != nil || ownerSecond.UserID != owner.UserID {
		t.Fatalf("second owner device = %#v, %v", ownerSecond, err)
	}
	other, err := authRepository.RegisterDevice(ctx, serverauth.GitHubUser{ID: 802, Login: "delete-other"}, serverauth.DeviceRegistration{
		UserID: "80000000-0000-4000-8000-000000000003", DeviceID: "80000000-0000-4000-8000-000000000013",
		Credential: serverauth.NewCredential{CredentialID: "80000000-0000-4000-8000-000000000023", SecretHash: otherHash, ExpiresAt: expires},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_revisions(revision, catalog_checksum, effective_at)
		VALUES ('synthetic-r1', $1, $2)
	`, bytes.Repeat([]byte{0x71}, 32), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := insertSyntheticBucket(ctx, pool, owner.DeviceID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", int64(10), "complete"); err != nil {
		t.Fatal(err)
	}
	if err := insertSyntheticBucket(ctx, pool, other.DeviceID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(10), "complete"); err != nil {
		t.Fatal(err)
	}
	groups, err := NewGroupRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	ownedGroupID := "80000000-0000-4000-8000-000000000031"
	if _, err := groups.CreateGroup(ctx, serverauth.Principal{UserID: owner.UserID}, servergroup.NewGroup{
		ID: ownedGroupID, Name: "Owned group", Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}
	if err := groups.CreateInvite(ctx, serverauth.Principal{UserID: owner.UserID}, servergroup.NewInvite{
		ID: "80000000-0000-4000-8000-000000000041", GroupID: ownedGroupID,
		CodeHash: sha256.Sum256([]byte("deletion-invite")), ExpiresAt: time.Now().UTC().Add(time.Hour), MaxUses: 2,
	}); err != nil {
		t.Fatal(err)
	}
	otherGroupID := "80000000-0000-4000-8000-000000000032"
	if _, err := groups.CreateGroup(ctx, serverauth.Principal{UserID: other.UserID}, servergroup.NewGroup{
		ID: otherGroupID, Name: "Retained group", Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_memberships(group_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'member')`, otherGroupID, owner.UserID); err != nil {
		t.Fatal(err)
	}
	accounts, err := NewAccountRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.DeleteAccount(ctx, serverauth.Principal{UserID: owner.UserID}); err != nil {
		t.Fatal(err)
	}
	assertDeletionCount(t, pool, `SELECT COUNT(*) FROM users WHERE id = $1::uuid`, owner.UserID, 0)
	assertDeletionCount(t, pool, `SELECT COUNT(*) FROM github_identities WHERE user_id = $1::uuid`, owner.UserID, 0)
	assertDeletionCount(t, pool, `SELECT COUNT(*) FROM devices WHERE user_id = $1::uuid`, owner.UserID, 0)
	assertDeletionCount(t, pool, `SELECT COUNT(*) FROM usage_buckets WHERE device_id IN ($1::uuid, $2::uuid)`, owner.DeviceID, ownerSecond.DeviceID, 0)
	assertDeletionCount(t, pool, `SELECT COUNT(*) FROM groups WHERE id = $1::uuid`, ownedGroupID, 0)
	assertDeletionCount(t, pool, `SELECT COUNT(*) FROM group_invites WHERE group_id = $1::uuid`, ownedGroupID, 0)
	assertDeletionCount(t, pool, `SELECT COUNT(*) FROM group_memberships WHERE user_id = $1::uuid`, owner.UserID, 0)
	assertDeletionCount(t, pool, `SELECT COUNT(*) FROM groups WHERE id = $1::uuid`, otherGroupID, 1)
	assertDeletionCount(t, pool, `SELECT COUNT(*) FROM usage_buckets WHERE device_id = $1::uuid`, other.DeviceID, 1)
	if _, err := authRepository.Authenticate(ctx, serverauth.CredentialProof{
		CredentialID: "80000000-0000-4000-8000-000000000021", SecretHash: ownerHash,
	}); !errors.Is(err, serverauth.ErrUnauthorized) {
		t.Fatalf("deleted credential error = %v", err)
	}
	if _, err := authRepository.Authenticate(ctx, serverauth.CredentialProof{
		CredentialID: "80000000-0000-4000-8000-000000000023", SecretHash: otherHash,
	}); err != nil {
		t.Fatalf("other account credential error = %v", err)
	}
	if err := accounts.DeleteAccount(ctx, serverauth.Principal{UserID: owner.UserID}); !errors.Is(err, serverauth.ErrUnauthorized) {
		t.Fatalf("repeated deletion error = %v", err)
	}
}

func assertDeletionCount(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, query string, arguments ...any) {
	t.Helper()
	want := arguments[len(arguments)-1].(int)
	arguments = arguments[:len(arguments)-1]
	var count int
	if err := pool.QueryRow(t.Context(), query, arguments...).Scan(&count); err != nil || count != want {
		t.Fatalf("deletion count = %d, %v, want %d", count, err, want)
	}
}

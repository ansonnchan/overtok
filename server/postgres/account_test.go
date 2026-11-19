package postgres

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/ansonnchan/tokenholic/internal/modelcatalog"
	protocol "github.com/ansonnchan/tokenholic/internal/sync"
	serveraccount "github.com/ansonnchan/tokenholic/server/account"
	serverauth "github.com/ansonnchan/tokenholic/server/auth"
)

func TestPostgresAccountAggregatesActiveDevicesAndRetainsRevokedHistory(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	pricing, err := modelcatalog.DefaultPricingRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsurePricingRevision(ctx, pool, pricing); err != nil {
		t.Fatal(err)
	}
	authRepository, err := NewAuthRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	firstHash := sha256.Sum256([]byte("account-first-secret"))
	secondHash := sha256.Sum256([]byte("account-second-secret"))
	expires := time.Now().UTC().Add(time.Hour)
	first, err := authRepository.RegisterDevice(ctx, serverauth.GitHubUser{ID: 701, Login: "account-user"}, serverauth.DeviceRegistration{
		UserID: "30000000-0000-4000-8000-000000000001", DeviceID: "30000000-0000-4000-8000-000000000011", ClientVersion: "test",
		Credential: serverauth.NewCredential{CredentialID: "30000000-0000-4000-8000-000000000021", SecretHash: firstHash, ExpiresAt: expires},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := authRepository.RegisterDevice(ctx, serverauth.GitHubUser{ID: 701, Login: "account-user"}, serverauth.DeviceRegistration{
		UserID: "30000000-0000-4000-8000-000000000002", DeviceID: "30000000-0000-4000-8000-000000000012", ClientVersion: "test",
		Credential: serverauth.NewCredential{CredentialID: "30000000-0000-4000-8000-000000000022", SecretHash: secondHash, ExpiresAt: expires},
	})
	if err != nil || first.UserID != second.UserID {
		t.Fatalf("second registration = %#v, %v", second, err)
	}
	usageRepository, err := NewUsageRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	firstEnvelope := localProtocolEnvelope(t, pricing.Revision)
	secondEnvelope := firstEnvelope
	secondOutput := uint64(11)
	secondEnvelope.Bucket.Tokens.Output.Value = &secondOutput
	firstPrincipal := serverauth.Principal{UserID: first.UserID, DeviceID: first.DeviceID}
	secondPrincipal := serverauth.Principal{UserID: second.UserID, DeviceID: second.DeviceID}
	if _, err := usageRepository.ApplyUsageBuckets(ctx, firstPrincipal, []protocol.Envelope{firstEnvelope}); err != nil {
		t.Fatal(err)
	}
	if _, err := usageRepository.ApplyUsageBuckets(ctx, secondPrincipal, []protocol.Envelope{secondEnvelope}); err != nil {
		t.Fatal(err)
	}
	accountRepository, err := NewAccountRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	accountRepository.now = func() time.Time { return time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC) }
	usage, err := accountRepository.AccountUsage(ctx, secondPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	assertAccountValue(t, usage.Tokens.Output, "18", serveraccount.AvailabilityComplete)
	assertAccountValue(t, usage.TotalTokens, "24", serveraccount.AvailabilityComplete)
	if usage.ActiveDevices != 2 || len(usage.Devices) != 2 || usage.LastSyncedAt == nil {
		t.Fatalf("account device state = %#v", usage)
	}
	if err := authRepository.RevokeDevice(ctx, serverauth.CredentialProof{
		CredentialID: "30000000-0000-4000-8000-000000000021", SecretHash: firstHash,
	}); err != nil {
		t.Fatal(err)
	}
	usage, err = accountRepository.AccountUsage(ctx, secondPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	assertAccountValue(t, usage.Tokens.Output, "11", serveraccount.AvailabilityComplete)
	if usage.ActiveDevices != 1 || len(usage.Devices) != 2 || !containsRevokedDevice(usage.Devices, first.DeviceID) {
		t.Fatalf("post-revocation device state = %#v", usage.Devices)
	}
	var retained int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM usage_buckets WHERE device_id = $1::uuid`, first.DeviceID).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("revoked device retained buckets = %d, %v", retained, err)
	}
	newer := secondEnvelope
	newer.Bucket.Revision = 2
	newerOutput := uint64(13)
	newer.Bucket.Tokens.Output.Value = &newerOutput
	if _, err := usageRepository.ApplyUsageBuckets(ctx, secondPrincipal, []protocol.Envelope{newer}); err != nil {
		t.Fatalf("active sibling upload: %v", err)
	}
	usage, err = accountRepository.AccountUsage(ctx, secondPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	assertAccountValue(t, usage.Tokens.Output, "13", serveraccount.AvailabilityComplete)
}

func assertAccountValue(t *testing.T, value serveraccount.Value, expected string, state serveraccount.Availability) {
	t.Helper()
	if value.Value == nil || *value.Value != expected || value.State != state {
		t.Fatalf("account value = %#v, want %q/%s", value, expected, state)
	}
}

func containsRevokedDevice(devices []serveraccount.Device, deviceID string) bool {
	for _, device := range devices {
		if device.DeviceID == deviceID {
			return device.Revoked
		}
	}
	return false
}

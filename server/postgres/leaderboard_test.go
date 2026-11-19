package postgres

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	serveraccount "github.com/ansonnchan/tokenholic/server/account"
	serverauth "github.com/ansonnchan/tokenholic/server/auth"
	servergroup "github.com/ansonnchan/tokenholic/server/group"
	"github.com/ansonnchan/tokenholic/server/leaderboard"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresLeaderboardPeriodsDevicesPricesAndTies(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner := serverauth.Principal{UserID: "60000000-0000-4000-8000-000000000001"}
	member := serverauth.Principal{UserID: "60000000-0000-4000-8000-000000000002"}
	outsider := serverauth.Principal{UserID: "60000000-0000-4000-8000-000000000003"}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id, display_name) VALUES
		($1::uuid, 'Alpha'), ($2::uuid, 'Beta'), ($3::uuid, 'Outside')
	`, owner.UserID, member.UserID, outsider.UserID); err != nil {
		t.Fatal(err)
	}
	groupRepository, err := NewGroupRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	groupID := "60000000-0000-4000-8000-000000000010"
	if _, err := groupRepository.CreateGroup(ctx, owner, servergroup.NewGroup{
		ID: groupID, Name: "Ranking group", Timezone: "America/Vancouver",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_memberships(group_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'member')`, groupID, member.UserID); err != nil {
		t.Fatal(err)
	}
	ownerDeviceOne := "60000000-0000-4000-8000-000000000011"
	ownerDeviceTwo := "60000000-0000-4000-8000-000000000012"
	ownerRevokedDevice := "60000000-0000-4000-8000-000000000013"
	memberDevice := "60000000-0000-4000-8000-000000000014"
	if _, err := pool.Exec(ctx, `
		INSERT INTO devices(id, user_id, revoked_at) VALUES
		($1::uuid, $2::uuid, NULL), ($3::uuid, $2::uuid, NULL),
		($4::uuid, $2::uuid, CURRENT_TIMESTAMP), ($5::uuid, $6::uuid, NULL)
	`, ownerDeviceOne, owner.UserID, ownerDeviceTwo, ownerRevokedDevice, memberDevice, member.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_revisions(revision, catalog_checksum, effective_at)
		VALUES ('leaderboard-r1', $1, $2)
	`, bytes.Repeat([]byte{0x61}, 32), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("America/Vancouver")
	if err != nil {
		t.Fatal(err)
	}
	dayStart := time.Date(2026, 11, 18, 0, 0, 0, 0, location).UTC()
	insertLeaderboardBucket(t, pool, leaderboardBucket{
		deviceID: ownerDeviceOne, key: strings.Repeat("a", 64), start: dayStart.Add(time.Minute),
		source: "claude", surface: "cli", observedModel: "model-a", canonicalModel: "model-a", modelProvider: "synthetic", pricingKey: "model-a", resolution: "resolved",
		input: 5, output: 5, estimate: int64Pointer(100), estimateState: "complete", pricingRevision: "leaderboard-r1",
	})
	insertLeaderboardBucket(t, pool, leaderboardBucket{
		deviceID: ownerDeviceTwo, key: strings.Repeat("b", 64), start: dayStart.Add(time.Hour),
		source: "codex", surface: "cli", observedModel: "route-model-b", canonicalModel: "model-b", modelProvider: "synthetic", pricingKey: "model-b", resolution: "inferred",
		input: 5, output: 5, estimate: int64Pointer(100), estimateState: "complete", pricingRevision: "leaderboard-r1",
	})
	insertLeaderboardBucket(t, pool, leaderboardBucket{
		deviceID: memberDevice, key: strings.Repeat("c", 64), start: dayStart.Add(10 * time.Minute),
		source: "codex", surface: "cli", observedModel: "unknown-model", resolution: "unknown",
		input: 10, output: 10, estimateState: "unavailable",
	})
	insertLeaderboardBucket(t, pool, leaderboardBucket{
		deviceID: ownerRevokedDevice, key: strings.Repeat("d", 64), start: dayStart.Add(20 * time.Minute),
		source: "claude", surface: "cli", observedModel: "model-a", canonicalModel: "model-a", modelProvider: "synthetic", pricingKey: "model-a", resolution: "resolved",
		input: 50, output: 50, estimate: int64Pointer(1000), estimateState: "complete", pricingRevision: "leaderboard-r1",
	})
	insertLeaderboardBucket(t, pool, leaderboardBucket{
		deviceID: ownerDeviceOne, key: strings.Repeat("e", 64), start: dayStart.Add(-time.Minute),
		source: "claude", surface: "cli", observedModel: "model-a", canonicalModel: "model-a", modelProvider: "synthetic", pricingKey: "model-a", resolution: "resolved",
		input: 50, output: 50, estimate: int64Pointer(1000), estimateState: "complete", pricingRevision: "leaderboard-r1",
	})

	repository, err := NewLeaderboardRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 16, 30, 0, 0, time.UTC)
	// Rank through the public service using a clock-aligned repository wrapper.
	service, err := leaderboard.NewService(staticLeaderboardAuthenticator{principal: owner}, fixedLeaderboardRepository{repository: repository, now: now})
	if err != nil {
		t.Fatal(err)
	}
	board, err := service.Get(ctx, "credential", groupID, leaderboard.PeriodToday, leaderboard.MetricTotalTokens)
	if err != nil {
		t.Fatal(err)
	}
	if board.Window.Start == nil || !board.Window.Start.Equal(dayStart) || len(board.Rows) != 2 {
		t.Fatalf("leaderboard window/rows = %#v", board)
	}
	alpha := leaderboardRowByUser(t, board.Rows, owner.UserID)
	beta := leaderboardRowByUser(t, board.Rows, member.UserID)
	assertLeaderboardValue(t, alpha.Value, "20", serveraccount.AvailabilityComplete)
	assertLeaderboardValue(t, beta.Value, "20", serveraccount.AvailabilityComplete)
	if alpha.Rank == nil || beta.Rank == nil || *alpha.Rank != 1 || *beta.Rank != 1 {
		t.Fatalf("tie ranks = %#v/%#v", alpha.Rank, beta.Rank)
	}
	if !alpha.CurrentUser || beta.CurrentUser || len(alpha.SourceMix) != 2 || alpha.MostUsedModel == nil || alpha.MostUsedModel.ModelID != "model-a" || alpha.LastSyncedAt == nil {
		t.Fatalf("alpha metadata = %#v", alpha)
	}
	if alpha.Limitations.InferredModelBuckets != 1 || beta.Limitations.UnknownModelBuckets != 1 || beta.Limitations.APIEstimateUnavailableBuckets != 1 {
		t.Fatalf("resolution limitations = %#v/%#v", alpha.Limitations, beta.Limitations)
	}
	costBoard, err := service.Get(ctx, "credential", groupID, leaderboard.PeriodToday, leaderboard.MetricAPIEstimate)
	if err != nil {
		t.Fatal(err)
	}
	alphaCost := leaderboardRowByUser(t, costBoard.Rows, owner.UserID)
	betaCost := leaderboardRowByUser(t, costBoard.Rows, member.UserID)
	assertLeaderboardValue(t, alphaCost.Value, "200", serveraccount.AvailabilityComplete)
	if betaCost.Value.Value != nil || betaCost.Value.State != serveraccount.AvailabilityUnavailable || betaCost.Rank != nil {
		t.Fatalf("unknown-price row = %#v", betaCost)
	}
	outputBoard, err := service.Get(ctx, "credential", groupID, leaderboard.PeriodToday, leaderboard.MetricOutput)
	if err != nil {
		t.Fatal(err)
	}
	assertLeaderboardValue(t, leaderboardRowByUser(t, outputBoard.Rows, owner.UserID).Value, "10", serveraccount.AvailabilityComplete)
	allTimeBoard, err := service.Get(ctx, "credential", groupID, leaderboard.PeriodAllTime, leaderboard.MetricTotalTokens)
	if err != nil {
		t.Fatal(err)
	}
	if allTimeBoard.Window.Start != nil {
		t.Fatalf("all-time start = %v", allTimeBoard.Window.Start)
	}
	assertLeaderboardValue(t, leaderboardRowByUser(t, allTimeBoard.Rows, owner.UserID).Value, "120", serveraccount.AvailabilityComplete)
	if _, err := repository.Leaderboard(ctx, outsider, leaderboard.Query{
		GroupID: groupID, Period: leaderboard.PeriodToday, Metric: leaderboard.MetricTotalTokens, GeneratedAt: now,
	}); !errors.Is(err, servergroup.ErrNotFound) {
		t.Fatalf("outsider leaderboard error = %v", err)
	}
}

type fixedLeaderboardRepository struct {
	repository *LeaderboardRepository
	now        time.Time
}

func (f fixedLeaderboardRepository) Leaderboard(ctx context.Context, principal serverauth.Principal, query leaderboard.Query) (leaderboard.Board, error) {
	query.GeneratedAt = f.now
	return f.repository.Leaderboard(ctx, principal, query)
}

type staticLeaderboardAuthenticator struct {
	principal serverauth.Principal
}

func (s staticLeaderboardAuthenticator) Authenticate(context.Context, string) (serverauth.Principal, error) {
	return s.principal, nil
}

type leaderboardBucket struct {
	deviceID        string
	key             string
	start           time.Time
	source          string
	surface         string
	observedModel   string
	canonicalModel  string
	modelProvider   string
	pricingKey      string
	resolution      string
	input           int64
	output          int64
	estimate        *int64
	estimateState   string
	pricingRevision string
}

func insertLeaderboardBucket(t *testing.T, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, bucket leaderboardBucket) {
	t.Helper()
	var canonicalModel, modelProvider, pricingKey, pricingRevision any
	if bucket.canonicalModel != "" {
		canonicalModel = bucket.canonicalModel
		modelProvider = bucket.modelProvider
		pricingKey = bucket.pricingKey
	}
	if bucket.pricingRevision != "" {
		pricingRevision = bucket.pricingRevision
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO usage_buckets(
			device_id, bucket_key, bucket_start, interval_seconds, source, surface,
			observed_model_id, canonical_model_id, model_provider, pricing_key,
			model_resolution, measurement, normalization_version,
			input_uncached_tokens, input_uncached_state,
			input_cache_read_tokens, input_cache_read_state,
			input_cache_write_tokens, input_cache_write_state,
			output_tokens, output_state,
			reasoning_output_tokens, reasoning_output_state,
			api_estimate_microusd, api_estimate_state, pricing_revision,
			revision, payload_hash
		) VALUES (
			$1::uuid, $2, $3, 60, $4, $5,
			$6, $7, $8, $9, $10, 'reported', '1',
			$11, 'complete', 0, 'complete', 0, 'complete', $12, 'complete',
			NULL, 'unavailable', $13, $14, $15,
			1, $16
		)
	`, bucket.deviceID, bucket.key, bucket.start, bucket.source, bucket.surface,
		bucket.observedModel, canonicalModel, modelProvider, pricingKey, bucket.resolution,
		bucket.input, bucket.output, bucket.estimate, bucket.estimateState, pricingRevision,
		bytes.Repeat([]byte{0x62}, 32)); err != nil {
		t.Fatal(err)
	}
}

func leaderboardRowByUser(t *testing.T, rows []leaderboard.Row, userID string) leaderboard.Row {
	t.Helper()
	for _, row := range rows {
		if row.UserID == userID {
			return row
		}
	}
	t.Fatalf("missing leaderboard user %s", userID)
	return leaderboard.Row{}
}

func assertLeaderboardValue(t *testing.T, value serveraccount.Value, want string, state serveraccount.Availability) {
	t.Helper()
	if value.Value == nil || *value.Value != want || value.State != state {
		t.Fatalf("value = %#v, want %s/%s", value, want, state)
	}
}

func int64Pointer(value int64) *int64 { return &value }

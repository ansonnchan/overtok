package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	serverauth "github.com/ansonnchan/overtok/server/auth"
	servergroup "github.com/ansonnchan/overtok/server/group"
	"github.com/ansonnchan/overtok/server/leaderboard"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LeaderboardRepository aggregates active group members and their active
// devices in one repeatable-read snapshot.
type LeaderboardRepository struct {
	pool *pgxpool.Pool
}

// NewLeaderboardRepository constructs the PostgreSQL leaderboard repository.
func NewLeaderboardRepository(pool *pgxpool.Pool) (*LeaderboardRepository, error) {
	if pool == nil {
		return nil, errors.New("initialize leaderboard repository: pool is required")
	}
	return &LeaderboardRepository{pool: pool}, nil
}

// Leaderboard reads one membership-authorized group ranking snapshot.
func (r *LeaderboardRepository) Leaderboard(ctx context.Context, principal serverauth.Principal, query leaderboard.Query) (board leaderboard.Board, err error) {
	board = leaderboard.Board{
		GroupID: query.GroupID, Period: query.Period, Metric: query.Metric,
		GeneratedAt: query.GeneratedAt.UTC(), Rows: make([]leaderboard.Row, 0),
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return board, fmt.Errorf("begin leaderboard snapshot: %w", err)
	}
	defer rollbackOnError(ctx, tx, &err)
	err = tx.QueryRow(ctx, `
		SELECT g.name, g.timezone
		FROM groups g
		JOIN group_memberships viewer ON viewer.group_id = g.id
		WHERE g.id = $1::uuid AND viewer.user_id = $2::uuid AND viewer.revoked_at IS NULL
	`, query.GroupID, principal.UserID).Scan(&board.GroupName, &board.Timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return board, servergroup.ErrNotFound
	}
	if err != nil {
		return board, fmt.Errorf("authorize leaderboard read: %w", err)
	}
	location, err := time.LoadLocation(board.Timezone)
	if err != nil {
		return board, fmt.Errorf("load group timezone: %w", err)
	}
	board.Window, err = leaderboard.WindowFor(query.Period, board.GeneratedAt, location)
	if err != nil {
		return board, err
	}
	var start any
	if board.Window.Start != nil {
		start = *board.Window.Start
	}
	rows, err := tx.Query(ctx, memberAggregateSQL, query.GroupID, start, board.Window.End)
	if err != nil {
		return board, fmt.Errorf("query leaderboard members: %w", err)
	}
	for rows.Next() {
		var row leaderboard.Row
		var totalBuckets int64
		var total, output, estimate aggregateScan
		if err := rows.Scan(
			&row.UserID, &row.DisplayName, &totalBuckets,
			&total.value, &total.known, &total.complete,
			&output.value, &output.known, &output.complete,
			&estimate.value, &estimate.known, &estimate.complete,
			&row.LastSyncedAt,
			&row.Limitations.UnknownModelBuckets,
			&row.Limitations.InferredModelBuckets,
			&row.Limitations.APIEstimateUnavailableBuckets,
		); err != nil {
			rows.Close()
			return board, fmt.Errorf("decode leaderboard member: %w", err)
		}
		switch query.Metric {
		case leaderboard.MetricTotalTokens:
			row.Value = total.coverage(totalBuckets)
		case leaderboard.MetricOutput:
			row.Value = output.coverage(totalBuckets)
		case leaderboard.MetricAPIEstimate:
			row.Value = estimate.coverage(totalBuckets)
		default:
			rows.Close()
			return board, servergroup.ErrInvalidInput
		}
		row.SourceMix = make([]leaderboard.SourceMix, 0)
		board.Rows = append(board.Rows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return board, fmt.Errorf("iterate leaderboard members: %w", err)
	}
	rows.Close()
	rowByUser := make(map[string]*leaderboard.Row, len(board.Rows))
	for index := range board.Rows {
		rowByUser[board.Rows[index].UserID] = &board.Rows[index]
	}
	if err = readSourceMix(ctx, tx, query.GroupID, start, board.Window.End, rowByUser); err != nil {
		return board, err
	}
	if err = readMostUsedModels(ctx, tx, query.GroupID, start, board.Window.End, rowByUser); err != nil {
		return board, err
	}
	if err = tx.Commit(ctx); err != nil {
		return board, fmt.Errorf("commit leaderboard snapshot: %w", err)
	}
	return board, nil
}

func readSourceMix(ctx context.Context, tx pgx.Tx, groupID string, start any, end time.Time, rowsByUser map[string]*leaderboard.Row) error {
	rows, err := tx.Query(ctx, sourceMixSQL, groupID, start, end)
	if err != nil {
		return fmt.Errorf("query leaderboard source mix: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		var mix leaderboard.SourceMix
		var totalBuckets int64
		var total aggregateScan
		if err := rows.Scan(&userID, &mix.Source, &totalBuckets, &total.value, &total.known, &total.complete); err != nil {
			return fmt.Errorf("decode leaderboard source mix: %w", err)
		}
		mix.TotalTokens = total.coverage(totalBuckets)
		if row := rowsByUser[userID]; row != nil {
			row.SourceMix = append(row.SourceMix, mix)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate leaderboard source mix: %w", err)
	}
	return nil
}

func readMostUsedModels(ctx context.Context, tx pgx.Tx, groupID string, start any, end time.Time, rowsByUser map[string]*leaderboard.Row) error {
	rows, err := tx.Query(ctx, mostUsedModelSQL, groupID, start, end)
	if err != nil {
		return fmt.Errorf("query leaderboard models: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]bool, len(rowsByUser))
	for rows.Next() {
		var userID string
		var model leaderboard.ModelUsage
		var totalBuckets int64
		var total aggregateScan
		if err := rows.Scan(&userID, &model.ModelID, &model.Resolution, &totalBuckets, &total.value, &total.known, &total.complete); err != nil {
			return fmt.Errorf("decode leaderboard model: %w", err)
		}
		if seen[userID] {
			continue
		}
		model.TotalTokens = total.coverage(totalBuckets)
		if row := rowsByUser[userID]; row != nil {
			row.MostUsedModel = &model
			seen[userID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate leaderboard models: %w", err)
	}
	return nil
}

const memberAggregateSQL = `
	WITH filtered AS (
		SELECT d.user_id, b.*
		FROM usage_buckets b
		JOIN devices d ON d.id = b.device_id
		WHERE d.revoked_at IS NULL
		  AND ($2::timestamptz IS NULL OR b.bucket_start >= $2)
		  AND b.bucket_start < $3
	), freshness AS (
		SELECT d.user_id, MAX(b.received_at) AS last_synced_at
		FROM devices d
		JOIN usage_buckets b ON b.device_id = d.id
		WHERE d.revoked_at IS NULL
		GROUP BY d.user_id
	)
	SELECT
		m.user_id::text,
		u.display_name,
		COUNT(f.bucket_key),
		SUM(CASE WHEN f.input_uncached_tokens IS NOT NULL OR f.input_cache_read_tokens IS NOT NULL OR f.input_cache_write_tokens IS NOT NULL OR f.output_tokens IS NOT NULL
			THEN COALESCE(f.input_uncached_tokens,0)+COALESCE(f.input_cache_read_tokens,0)+COALESCE(f.input_cache_write_tokens,0)+COALESCE(f.output_tokens,0) END)::text,
		COUNT(f.bucket_key) FILTER (WHERE f.input_uncached_tokens IS NOT NULL OR f.input_cache_read_tokens IS NOT NULL OR f.input_cache_write_tokens IS NOT NULL OR f.output_tokens IS NOT NULL),
		COALESCE(BOOL_AND(f.input_uncached_state='complete' AND f.input_cache_read_state='complete' AND f.input_cache_write_state='complete' AND f.output_state='complete') FILTER (WHERE f.bucket_key IS NOT NULL), false),
		SUM(f.output_tokens)::text,
		COUNT(f.output_tokens),
		COALESCE(BOOL_AND(f.output_state='complete') FILTER (WHERE f.bucket_key IS NOT NULL), false),
		SUM(f.api_estimate_microusd)::text,
		COUNT(f.api_estimate_microusd),
		COALESCE(BOOL_AND(f.api_estimate_state='complete') FILTER (WHERE f.bucket_key IS NOT NULL), false),
		fr.last_synced_at,
		COUNT(f.bucket_key) FILTER (WHERE f.model_resolution='unknown'),
		COUNT(f.bucket_key) FILTER (WHERE f.model_resolution='inferred'),
		COUNT(f.bucket_key) FILTER (WHERE f.api_estimate_microusd IS NULL)
	FROM group_memberships m
	JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL
	LEFT JOIN filtered f ON f.user_id = m.user_id
	LEFT JOIN freshness fr ON fr.user_id = m.user_id
	WHERE m.group_id = $1::uuid AND m.revoked_at IS NULL
	GROUP BY m.user_id, u.display_name, fr.last_synced_at
`

const sourceMixSQL = `
	WITH filtered AS (
		SELECT d.user_id, b.*
		FROM usage_buckets b
		JOIN devices d ON d.id = b.device_id
		WHERE d.revoked_at IS NULL
		  AND ($2::timestamptz IS NULL OR b.bucket_start >= $2)
		  AND b.bucket_start < $3
	)
	SELECT
		f.user_id::text,
		f.source,
		COUNT(*),
		SUM(CASE WHEN f.input_uncached_tokens IS NOT NULL OR f.input_cache_read_tokens IS NOT NULL OR f.input_cache_write_tokens IS NOT NULL OR f.output_tokens IS NOT NULL
			THEN COALESCE(f.input_uncached_tokens,0)+COALESCE(f.input_cache_read_tokens,0)+COALESCE(f.input_cache_write_tokens,0)+COALESCE(f.output_tokens,0) END)::text,
		COUNT(*) FILTER (WHERE f.input_uncached_tokens IS NOT NULL OR f.input_cache_read_tokens IS NOT NULL OR f.input_cache_write_tokens IS NOT NULL OR f.output_tokens IS NOT NULL),
		COALESCE(BOOL_AND(f.input_uncached_state='complete' AND f.input_cache_read_state='complete' AND f.input_cache_write_state='complete' AND f.output_state='complete'), false)
	FROM filtered f
	JOIN group_memberships m ON m.user_id = f.user_id AND m.group_id = $1::uuid AND m.revoked_at IS NULL
	GROUP BY f.user_id, f.source
	ORDER BY f.user_id, f.source
`

const mostUsedModelSQL = `
	WITH filtered AS (
		SELECT d.user_id, b.*
		FROM usage_buckets b
		JOIN devices d ON d.id = b.device_id
		WHERE d.revoked_at IS NULL
		  AND ($2::timestamptz IS NULL OR b.bucket_start >= $2)
		  AND b.bucket_start < $3
	)
	SELECT
		f.user_id::text,
		COALESCE(f.canonical_model_id, f.observed_model_id) AS model_id,
		f.model_resolution,
		COUNT(*),
		SUM(CASE WHEN f.input_uncached_tokens IS NOT NULL OR f.input_cache_read_tokens IS NOT NULL OR f.input_cache_write_tokens IS NOT NULL OR f.output_tokens IS NOT NULL
			THEN COALESCE(f.input_uncached_tokens,0)+COALESCE(f.input_cache_read_tokens,0)+COALESCE(f.input_cache_write_tokens,0)+COALESCE(f.output_tokens,0) END)::text AS total_tokens,
		COUNT(*) FILTER (WHERE f.input_uncached_tokens IS NOT NULL OR f.input_cache_read_tokens IS NOT NULL OR f.input_cache_write_tokens IS NOT NULL OR f.output_tokens IS NOT NULL),
		COALESCE(BOOL_AND(f.input_uncached_state='complete' AND f.input_cache_read_state='complete' AND f.input_cache_write_state='complete' AND f.output_state='complete'), false)
	FROM filtered f
	JOIN group_memberships m ON m.user_id = f.user_id AND m.group_id = $1::uuid AND m.revoked_at IS NULL
	GROUP BY f.user_id, model_id, f.model_resolution
	HAVING SUM(CASE WHEN f.input_uncached_tokens IS NOT NULL OR f.input_cache_read_tokens IS NOT NULL OR f.input_cache_write_tokens IS NOT NULL OR f.output_tokens IS NOT NULL
		THEN COALESCE(f.input_uncached_tokens,0)+COALESCE(f.input_cache_read_tokens,0)+COALESCE(f.input_cache_write_tokens,0)+COALESCE(f.output_tokens,0) END) IS NOT NULL
	ORDER BY f.user_id, SUM(CASE WHEN f.input_uncached_tokens IS NOT NULL OR f.input_cache_read_tokens IS NOT NULL OR f.input_cache_write_tokens IS NOT NULL OR f.output_tokens IS NOT NULL
		THEN COALESCE(f.input_uncached_tokens,0)+COALESCE(f.input_cache_read_tokens,0)+COALESCE(f.input_cache_write_tokens,0)+COALESCE(f.output_tokens,0) END) DESC,
		model_id, f.model_resolution
`

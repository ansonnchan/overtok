package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	protocol "github.com/ansonnchan/overtok/internal/sync"
	serverauth "github.com/ansonnchan/overtok/server/auth"
	serversync "github.com/ansonnchan/overtok/server/sync"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UsageRepository applies absolute device-bucket revisions.
type UsageRepository struct {
	pool *pgxpool.Pool
}

// NewUsageRepository constructs the PostgreSQL synchronization repository.
func NewUsageRepository(pool *pgxpool.Pool) (*UsageRepository, error) {
	if pool == nil {
		return nil, errors.New("initialize usage repository: pool is required")
	}
	return &UsageRepository{pool: pool}, nil
}

// ApplyUsageBuckets applies a batch atomically. Equal revisions are no-ops,
// older revisions are stale, and only newer absolute values replace storage.
func (r *UsageRepository) ApplyUsageBuckets(ctx context.Context, principal serverauth.Principal, buckets []protocol.Envelope) (acknowledgements []protocol.Acknowledgement, err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin usage synchronization: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	var active int
	if err = tx.QueryRow(ctx, `
		SELECT 1
		FROM devices d JOIN users u ON u.id = d.user_id
		WHERE d.id = $1::uuid AND d.user_id = $2::uuid
		  AND d.revoked_at IS NULL AND u.deleted_at IS NULL
		FOR SHARE OF d, u
	`, principal.DeviceID, principal.UserID).Scan(&active); errors.Is(err, pgx.ErrNoRows) {
		return nil, serverauth.ErrUnauthorized
	} else if err != nil {
		return nil, fmt.Errorf("verify active usage device: %w", err)
	}
	ordered := append([]protocol.Envelope(nil), buckets...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].BucketKey < ordered[j].BucketKey })
	acknowledgements = make([]protocol.Acknowledgement, 0, len(ordered))
	for _, envelope := range ordered {
		_, payloadHash, hashErr := protocol.CanonicalEnvelope(envelope)
		if hashErr != nil {
			return nil, hashErr
		}
		var currentRevisionText string
		var currentHash []byte
		readErr := tx.QueryRow(ctx, `
			SELECT revision::text, payload_hash
			FROM usage_buckets
			WHERE device_id = $1::uuid AND bucket_key = $2
			FOR UPDATE
		`, principal.DeviceID, envelope.BucketKey).Scan(&currentRevisionText, &currentHash)
		status := protocol.AckApplied
		switch {
		case errors.Is(readErr, pgx.ErrNoRows):
			if err = insertUsageBucket(ctx, tx, principal.DeviceID, envelope, payloadHash); err != nil {
				return nil, err
			}
		case readErr != nil:
			return nil, fmt.Errorf("read server usage revision: %w", readErr)
		default:
			currentRevision, parseErr := strconv.ParseUint(currentRevisionText, 10, 64)
			if parseErr != nil {
				return nil, fmt.Errorf("decode server usage revision: %w", parseErr)
			}
			switch {
			case envelope.Bucket.Revision < currentRevision:
				status = protocol.AckStale
			case envelope.Bucket.Revision == currentRevision:
				if !bytes.Equal(currentHash, payloadHash[:]) {
					return nil, serversync.ErrRevisionConflict
				}
				status = protocol.AckUnchanged
			default:
				if err = updateUsageBucket(ctx, tx, principal.DeviceID, envelope, payloadHash); err != nil {
					return nil, err
				}
			}
		}
		acknowledgements = append(acknowledgements, protocol.Acknowledgement{
			BucketKey: envelope.BucketKey, Revision: envelope.Bucket.Revision, Status: status,
		})
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit usage synchronization: %w", err)
	}
	return acknowledgements, nil
}

func insertUsageBucket(ctx context.Context, tx pgx.Tx, deviceID string, envelope protocol.Envelope, payloadHash [32]byte) error {
	arguments := usageBucketArguments(deviceID, envelope, payloadHash)
	_, err := tx.Exec(ctx, `
		INSERT INTO usage_buckets(
			device_id, bucket_key, bucket_start, interval_seconds, source, surface,
			observed_model_id, canonical_model_id, model_provider, pricing_key,
			model_resolution, measurement, normalization_version,
			input_uncached_tokens, input_uncached_state,
			input_cache_read_tokens, input_cache_read_state,
			input_cache_write_tokens, input_cache_write_state,
			output_tokens, output_state, reasoning_output_tokens, reasoning_output_state,
			api_estimate_microusd, api_estimate_state, pricing_revision, revision, payload_hash
		) VALUES (
			$1::uuid,$2,$3,60,$4,$5,$6,$7,$8,$9,$10,$11,$12,
			$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27
		)
	`, arguments...)
	if err != nil {
		return fmt.Errorf("insert server usage bucket: %w", err)
	}
	return nil
}

func updateUsageBucket(ctx context.Context, tx pgx.Tx, deviceID string, envelope protocol.Envelope, payloadHash [32]byte) error {
	arguments := usageBucketArguments(deviceID, envelope, payloadHash)
	_, err := tx.Exec(ctx, `
		UPDATE usage_buckets SET
			bucket_start=$3, interval_seconds=60, source=$4, surface=$5,
			observed_model_id=$6, canonical_model_id=$7, model_provider=$8, pricing_key=$9,
			model_resolution=$10, measurement=$11, normalization_version=$12,
			input_uncached_tokens=$13, input_uncached_state=$14,
			input_cache_read_tokens=$15, input_cache_read_state=$16,
			input_cache_write_tokens=$17, input_cache_write_state=$18,
			output_tokens=$19, output_state=$20,
			reasoning_output_tokens=$21, reasoning_output_state=$22,
			api_estimate_microusd=$23, api_estimate_state=$24,
			pricing_revision=$25, revision=$26, payload_hash=$27, received_at=CURRENT_TIMESTAMP
		WHERE device_id=$1::uuid AND bucket_key=$2
	`, arguments...)
	if err != nil {
		return fmt.Errorf("replace server usage bucket: %w", err)
	}
	return nil
}

func usageBucketArguments(deviceID string, envelope protocol.Envelope, payloadHash [32]byte) []any {
	bucket := envelope.Bucket
	return []any{
		deviceID, envelope.BucketKey, bucket.Start, bucket.Source, bucket.Surface,
		bucket.ObservedModelID, bucket.CanonicalModelID, bucket.ModelProvider, bucket.PricingKey,
		bucket.ModelResolution, bucket.Measurement, bucket.NormalizationVersion,
		numericValue(bucket.Tokens.InputUncached), bucket.Tokens.InputUncached.State,
		numericValue(bucket.Tokens.InputCacheRead), bucket.Tokens.InputCacheRead.State,
		numericValue(bucket.Tokens.InputCacheWrite), bucket.Tokens.InputCacheWrite.State,
		numericValue(bucket.Tokens.Output), bucket.Tokens.Output.State,
		numericValue(bucket.Tokens.ReasoningOutput), bucket.Tokens.ReasoningOutput.State,
		numericValue(bucket.APIEstimate), bucket.APIEstimate.State, bucket.PricingRevision,
		strconv.FormatUint(bucket.Revision, 10), payloadHash[:],
	}
}

func numericValue(value protocol.Value) any {
	if value.Value == nil {
		return nil
	}
	return strconv.FormatUint(*value.Value, 10)
}

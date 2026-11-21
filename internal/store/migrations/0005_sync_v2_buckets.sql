-- Sync v2 uploads observed evidence only, so buckets are rebuilt in that shape.
-- They are derived state: the next refresh recreates every bucket from stored
-- observations and uploads it again under its new key.
DROP TABLE outbox_records;
DROP TABLE usage_buckets;

CREATE TABLE usage_buckets (
    bucket_key TEXT PRIMARY KEY,
    bucket_start TEXT NOT NULL,
    revision TEXT NOT NULL CHECK (length(revision) > 0 AND revision NOT GLOB '*[^0-9]*'),
    -- Digest of the uploadable content without its revision. An unchanged
    -- bucket keeps its revision instead of being uploaded again.
    content_hash TEXT NOT NULL CHECK (length(content_hash) = 64),
    -- Exactly what is uploaded: observed model, evidence quality, and tokens.
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    -- Local-only catalog resolution and estimate. Never uploaded.
    preview_json TEXT NOT NULL CHECK (json_valid(preview_json)),
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX usage_buckets_period ON usage_buckets(bucket_start, bucket_key);

CREATE TABLE outbox_records (
    bucket_key TEXT PRIMARY KEY REFERENCES usage_buckets(bucket_key) ON DELETE CASCADE,
    revision TEXT NOT NULL CHECK (length(revision) > 0 AND revision NOT GLOB '*[^0-9]*'),
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'acknowledged')),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

-- Observations keep source evidence only. Catalog results were stored at
-- insert time and went stale; they are now derived per bucket.
UPDATE observations SET
    canonical_model_id = NULL,
    model_provider = NULL,
    pricing_key = NULL,
    model_resolution = CASE
        WHEN observed_model_id = '' THEN 'unknown'
        WHEN warnings_json LIKE '%"model_inferred"%' THEN 'inferred'
        ELSE 'reported'
    END;

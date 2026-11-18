CREATE TABLE usage_buckets (
    bucket_key TEXT PRIMARY KEY,
    bucket_start TEXT NOT NULL,
    interval_seconds INTEGER NOT NULL CHECK (interval_seconds = 60),
    source TEXT NOT NULL CHECK (source IN ('claude', 'codex')),
    surface TEXT NOT NULL CHECK (surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork', 'desktop', 'shared_local')),
    observed_model_id TEXT NOT NULL,
    canonical_model_id TEXT,
    model_provider TEXT,
    pricing_key TEXT,
    model_resolution TEXT NOT NULL CHECK (model_resolution IN ('reported', 'resolved', 'inferred', 'unknown')),
    measurement TEXT NOT NULL CHECK (measurement IN ('reported', 'derived', 'estimated')),
    normalization_version TEXT NOT NULL CHECK (normalization_version <> ''),
    input_uncached_tokens TEXT CHECK (input_uncached_tokens IS NULL OR (length(input_uncached_tokens) > 0 AND input_uncached_tokens NOT GLOB '*[^0-9]*')),
    input_uncached_state TEXT NOT NULL CHECK (input_uncached_state IN ('complete', 'partial', 'unavailable')),
    input_cache_read_tokens TEXT CHECK (input_cache_read_tokens IS NULL OR (length(input_cache_read_tokens) > 0 AND input_cache_read_tokens NOT GLOB '*[^0-9]*')),
    input_cache_read_state TEXT NOT NULL CHECK (input_cache_read_state IN ('complete', 'partial', 'unavailable')),
    input_cache_write_tokens TEXT CHECK (input_cache_write_tokens IS NULL OR (length(input_cache_write_tokens) > 0 AND input_cache_write_tokens NOT GLOB '*[^0-9]*')),
    input_cache_write_state TEXT NOT NULL CHECK (input_cache_write_state IN ('complete', 'partial', 'unavailable')),
    output_tokens TEXT CHECK (output_tokens IS NULL OR (length(output_tokens) > 0 AND output_tokens NOT GLOB '*[^0-9]*')),
    output_state TEXT NOT NULL CHECK (output_state IN ('complete', 'partial', 'unavailable')),
    reasoning_output_tokens TEXT CHECK (reasoning_output_tokens IS NULL OR (length(reasoning_output_tokens) > 0 AND reasoning_output_tokens NOT GLOB '*[^0-9]*')),
    reasoning_output_state TEXT NOT NULL CHECK (reasoning_output_state IN ('complete', 'partial', 'unavailable')),
    api_estimate_microusd TEXT CHECK (api_estimate_microusd IS NULL OR (length(api_estimate_microusd) > 0 AND api_estimate_microusd NOT GLOB '*[^0-9]*')),
    api_estimate_state TEXT NOT NULL CHECK (api_estimate_state IN ('complete', 'partial', 'unavailable')),
    pricing_revision TEXT,
    revision TEXT NOT NULL CHECK (length(revision) > 0 AND revision NOT GLOB '*[^0-9]*'),
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (
        (source = 'claude' AND surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork')) OR
        (source = 'codex' AND surface IN ('cli', 'desktop', 'shared_local'))
    )
) STRICT;

CREATE INDEX usage_buckets_period ON usage_buckets(bucket_start, bucket_key);
CREATE INDEX usage_buckets_source ON usage_buckets(source, surface, bucket_start);
CREATE INDEX usage_buckets_model ON usage_buckets(canonical_model_id, observed_model_id, bucket_start);

CREATE TABLE outbox_records (
    bucket_key TEXT PRIMARY KEY REFERENCES usage_buckets(bucket_key) ON DELETE CASCADE,
    revision TEXT NOT NULL CHECK (length(revision) > 0 AND revision NOT GLOB '*[^0-9]*'),
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'acknowledged')),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

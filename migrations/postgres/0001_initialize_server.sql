-- Server state contains identities and privacy-safe absolute usage only.
-- Raw source records and local identifiers have no server columns.

CREATE TABLE users (
    id UUID PRIMARY KEY,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 80),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE TABLE github_identities (
    github_user_id BIGINT PRIMARY KEY CHECK (github_user_id > 0),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    login TEXT NOT NULL CHECK (char_length(login) BETWEEN 1 AND 39 AND login ~ '^[A-Za-z0-9-]+$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE devices (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_version TEXT CHECK (client_version IS NULL OR char_length(client_version) BETWEEN 1 AND 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    CHECK (last_seen_at IS NULL OR last_seen_at >= created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX devices_user_id ON devices(user_id);

CREATE TABLE device_credentials (
    id UUID PRIMARY KEY,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    secret_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(secret_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (last_used_at IS NULL OR last_used_at >= created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX device_credentials_device_id ON device_credentials(device_id);

CREATE TABLE pricing_revisions (
    revision TEXT PRIMARY KEY CHECK (char_length(revision) BETWEEN 1 AND 64),
    catalog_checksum BYTEA NOT NULL CHECK (octet_length(catalog_checksum) = 32),
    effective_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE usage_buckets (
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    bucket_key TEXT NOT NULL CHECK (bucket_key ~ '^[0-9a-f]{64}$'),
    bucket_start TIMESTAMPTZ NOT NULL CHECK (date_trunc('minute', bucket_start) = bucket_start),
    interval_seconds INTEGER NOT NULL CHECK (interval_seconds = 60),
    source TEXT NOT NULL CHECK (source IN ('claude', 'codex')),
    surface TEXT NOT NULL CHECK (surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork', 'desktop', 'shared_local')),
    observed_model_id TEXT NOT NULL CHECK (char_length(observed_model_id) BETWEEN 1 AND 256),
    canonical_model_id TEXT CHECK (canonical_model_id IS NULL OR char_length(canonical_model_id) BETWEEN 1 AND 256),
    model_provider TEXT CHECK (model_provider IS NULL OR char_length(model_provider) BETWEEN 1 AND 64),
    pricing_key TEXT CHECK (pricing_key IS NULL OR char_length(pricing_key) BETWEEN 1 AND 256),
    model_resolution TEXT NOT NULL CHECK (model_resolution IN ('reported', 'resolved', 'inferred', 'unknown')),
    measurement TEXT NOT NULL CHECK (measurement IN ('reported', 'derived', 'estimated')),
    normalization_version TEXT NOT NULL CHECK (char_length(normalization_version) BETWEEN 1 AND 64),
    input_uncached_tokens NUMERIC(20, 0),
    input_uncached_state TEXT NOT NULL CHECK (input_uncached_state IN ('complete', 'partial', 'unavailable')),
    input_cache_read_tokens NUMERIC(20, 0),
    input_cache_read_state TEXT NOT NULL CHECK (input_cache_read_state IN ('complete', 'partial', 'unavailable')),
    input_cache_write_tokens NUMERIC(20, 0),
    input_cache_write_state TEXT NOT NULL CHECK (input_cache_write_state IN ('complete', 'partial', 'unavailable')),
    output_tokens NUMERIC(20, 0),
    output_state TEXT NOT NULL CHECK (output_state IN ('complete', 'partial', 'unavailable')),
    reasoning_output_tokens NUMERIC(20, 0),
    reasoning_output_state TEXT NOT NULL CHECK (reasoning_output_state IN ('complete', 'partial', 'unavailable')),
    api_estimate_microusd NUMERIC(20, 0),
    api_estimate_state TEXT NOT NULL CHECK (api_estimate_state IN ('complete', 'partial', 'unavailable')),
    pricing_revision TEXT REFERENCES pricing_revisions(revision) ON UPDATE CASCADE ON DELETE RESTRICT,
    revision NUMERIC(20, 0) NOT NULL CHECK (revision BETWEEN 1 AND 18446744073709551615),
    payload_hash BYTEA NOT NULL CHECK (octet_length(payload_hash) = 32),
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (device_id, bucket_key),
    CHECK (
        (source = 'claude' AND surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork')) OR
        (source = 'codex' AND surface IN ('cli', 'desktop', 'shared_local'))
    ),
    CHECK (
        (model_resolution = 'unknown' AND canonical_model_id IS NULL AND model_provider IS NULL AND pricing_key IS NULL) OR
        (model_resolution <> 'unknown' AND canonical_model_id IS NOT NULL AND model_provider IS NOT NULL)
    ),
    CHECK (
        (input_uncached_state = 'unavailable' AND input_uncached_tokens IS NULL) OR
        (input_uncached_state IN ('complete', 'partial') AND input_uncached_tokens IS NOT NULL AND input_uncached_tokens BETWEEN 0 AND 18446744073709551615)
    ),
    CHECK (
        (input_cache_read_state = 'unavailable' AND input_cache_read_tokens IS NULL) OR
        (input_cache_read_state IN ('complete', 'partial') AND input_cache_read_tokens IS NOT NULL AND input_cache_read_tokens BETWEEN 0 AND 18446744073709551615)
    ),
    CHECK (
        (input_cache_write_state = 'unavailable' AND input_cache_write_tokens IS NULL) OR
        (input_cache_write_state IN ('complete', 'partial') AND input_cache_write_tokens IS NOT NULL AND input_cache_write_tokens BETWEEN 0 AND 18446744073709551615)
    ),
    CHECK (
        (output_state = 'unavailable' AND output_tokens IS NULL) OR
        (output_state IN ('complete', 'partial') AND output_tokens IS NOT NULL AND output_tokens BETWEEN 0 AND 18446744073709551615)
    ),
    CHECK (
        (reasoning_output_state = 'unavailable' AND reasoning_output_tokens IS NULL) OR
        (reasoning_output_state IN ('complete', 'partial') AND reasoning_output_tokens IS NOT NULL AND reasoning_output_tokens BETWEEN 0 AND 18446744073709551615)
    ),
    CHECK (
        (api_estimate_state = 'unavailable' AND api_estimate_microusd IS NULL) OR
        (api_estimate_state IN ('complete', 'partial') AND api_estimate_microusd IS NOT NULL AND api_estimate_microusd BETWEEN 0 AND 18446744073709551615 AND pricing_revision IS NOT NULL)
    )
);

CREATE INDEX usage_buckets_period ON usage_buckets(bucket_start, device_id);
CREATE INDEX usage_buckets_model ON usage_buckets(canonical_model_id, observed_model_id, bucket_start);

COMMENT ON TABLE device_credentials IS 'Revocable device credentials; only one-way secret hashes are stored.';
COMMENT ON TABLE usage_buckets IS 'Privacy-limited absolute device buckets; raw observations and local source identities are excluded.';

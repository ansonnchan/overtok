-- Sync v2: the server owns model resolution and pricing.
--
-- This migration is forward-only and REPLACES usage_buckets. Uploaded demo
-- usage is discarded rather than translated: v1 rows mixed client conclusions
-- into bucket identity and carried no cache-write lifetimes, so they cannot be
-- re-derived. Clients rebuild and upload their history again after upgrading.
-- Accounts, devices, groups, and memberships are untouched.

DROP TABLE usage_buckets;

CREATE TABLE usage_buckets (
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    bucket_key TEXT NOT NULL CHECK (bucket_key ~ '^[0-9a-f]{64}$'),
    bucket_start TIMESTAMPTZ NOT NULL CHECK (date_trunc('minute', bucket_start) = bucket_start),
    interval_seconds INTEGER NOT NULL CHECK (interval_seconds = 60),

    -- Client evidence. The API validates which sources and surfaces exist;
    -- the schema checks only their shape, so adding a source needs no migration.
    source TEXT NOT NULL CHECK (source ~ '^[a-z][a-z0-9_]{0,31}$'),
    surface TEXT NOT NULL CHECK (surface ~ '^[a-z][a-z0-9_]{0,31}$'),
    observed_model_id TEXT NOT NULL CHECK (char_length(observed_model_id) <= 256),
    model_evidence TEXT NOT NULL CHECK (model_evidence IN ('reported', 'inferred', 'unknown')),
    measurement TEXT NOT NULL CHECK (measurement IN ('reported', 'derived', 'estimated')),
    normalization_version TEXT NOT NULL CHECK (char_length(normalization_version) BETWEEN 1 AND 64),
    input_uncached_tokens NUMERIC(20, 0),
    input_uncached_state TEXT NOT NULL CHECK (input_uncached_state IN ('complete', 'partial', 'unavailable')),
    input_cache_read_tokens NUMERIC(20, 0),
    input_cache_read_state TEXT NOT NULL CHECK (input_cache_read_state IN ('complete', 'partial', 'unavailable')),
    input_cache_write_tokens NUMERIC(20, 0),
    input_cache_write_state TEXT NOT NULL CHECK (input_cache_write_state IN ('complete', 'partial', 'unavailable')),
    input_cache_write_5m_tokens NUMERIC(20, 0),
    input_cache_write_5m_state TEXT NOT NULL CHECK (input_cache_write_5m_state IN ('complete', 'partial', 'unavailable')),
    input_cache_write_1h_tokens NUMERIC(20, 0),
    input_cache_write_1h_state TEXT NOT NULL CHECK (input_cache_write_1h_state IN ('complete', 'partial', 'unavailable')),
    output_tokens NUMERIC(20, 0),
    output_state TEXT NOT NULL CHECK (output_state IN ('complete', 'partial', 'unavailable')),
    reasoning_output_tokens NUMERIC(20, 0),
    reasoning_output_state TEXT NOT NULL CHECK (reasoning_output_state IN ('complete', 'partial', 'unavailable')),
    revision NUMERIC(20, 0) NOT NULL CHECK (revision BETWEEN 1 AND 18446744073709551615),
    payload_hash BYTEA NOT NULL CHECK (octet_length(payload_hash) = 32),
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Server-derived. Every column below is a pure function of the evidence
    -- above and pricing_revision, so it can be recomputed at any time.
    canonical_model_id TEXT CHECK (canonical_model_id IS NULL OR char_length(canonical_model_id) BETWEEN 1 AND 256),
    model_provider TEXT CHECK (model_provider IS NULL OR char_length(model_provider) BETWEEN 1 AND 64),
    pricing_key TEXT CHECK (pricing_key IS NULL OR char_length(pricing_key) BETWEEN 1 AND 256),
    model_resolution TEXT NOT NULL CHECK (model_resolution IN ('reported', 'resolved', 'inferred', 'unknown')),
    model_match TEXT NOT NULL CHECK (model_match IN ('exact', 'alias', 'reviewed', 'unknown')),
    unpriced_reason TEXT CHECK (unpriced_reason IS NULL OR char_length(unpriced_reason) BETWEEN 1 AND 64),
    priced_tokens NUMERIC(20, 0) NOT NULL CHECK (priced_tokens BETWEEN 0 AND 18446744073709551615),
    unpriced_tokens NUMERIC(20, 0) NOT NULL CHECK (unpriced_tokens BETWEEN 0 AND 18446744073709551615),
    api_estimate_microusd NUMERIC(20, 0),
    api_estimate_state TEXT NOT NULL CHECK (api_estimate_state IN ('complete', 'partial', 'unavailable')),
    pricing_revision TEXT NOT NULL REFERENCES pricing_revisions(revision) ON UPDATE CASCADE ON DELETE RESTRICT,

    PRIMARY KEY (device_id, bucket_key),
    CHECK (model_evidence <> 'unknown' OR model_resolution = 'unknown'),
    CHECK (model_evidence = 'unknown' OR observed_model_id <> ''),
    CHECK (
        (model_resolution = 'unknown' AND model_match = 'unknown' AND canonical_model_id IS NULL AND model_provider IS NULL AND pricing_key IS NULL) OR
        (model_resolution <> 'unknown' AND model_match <> 'unknown' AND canonical_model_id IS NOT NULL AND model_provider IS NOT NULL)
    ),
    -- A bucket without a price always says why.
    CHECK ((pricing_key IS NULL) = (unpriced_reason IS NOT NULL)),
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
        (input_cache_write_5m_state = 'unavailable' AND input_cache_write_5m_tokens IS NULL) OR
        (input_cache_write_5m_state IN ('complete', 'partial') AND input_cache_write_5m_tokens IS NOT NULL AND input_cache_write_5m_tokens BETWEEN 0 AND 18446744073709551615)
    ),
    CHECK (
        (input_cache_write_1h_state = 'unavailable' AND input_cache_write_1h_tokens IS NULL) OR
        (input_cache_write_1h_state IN ('complete', 'partial') AND input_cache_write_1h_tokens IS NOT NULL AND input_cache_write_1h_tokens BETWEEN 0 AND 18446744073709551615)
    ),
    CHECK (
        (output_state = 'unavailable' AND output_tokens IS NULL) OR
        (output_state IN ('complete', 'partial') AND output_tokens IS NOT NULL AND output_tokens BETWEEN 0 AND 18446744073709551615)
    ),
    CHECK (
        (reasoning_output_state = 'unavailable' AND reasoning_output_tokens IS NULL) OR
        (reasoning_output_state IN ('complete', 'partial') AND reasoning_output_tokens IS NOT NULL AND reasoning_output_tokens BETWEEN 0 AND 18446744073709551615)
    ),
    -- Cache-write lifetimes split the total; they never exceed or replace it.
    CHECK (
        (input_cache_write_5m_tokens IS NULL AND input_cache_write_1h_tokens IS NULL) OR
        (input_cache_write_tokens IS NOT NULL AND
         COALESCE(input_cache_write_5m_tokens, 0) + COALESCE(input_cache_write_1h_tokens, 0) <= input_cache_write_tokens)
    ),
    CHECK (reasoning_output_tokens IS NULL OR output_tokens IS NULL OR reasoning_output_tokens <= output_tokens),
    -- Priced and unpriced tokens partition the known total, so cost coverage
    -- is exact without re-reading the categories.
    CHECK (
        priced_tokens + unpriced_tokens =
        COALESCE(input_uncached_tokens, 0) + COALESCE(input_cache_read_tokens, 0) +
        COALESCE(input_cache_write_tokens, 0) + COALESCE(output_tokens, 0)
    ),
    CHECK (
        (api_estimate_state = 'unavailable' AND api_estimate_microusd IS NULL AND priced_tokens = 0) OR
        (api_estimate_state IN ('complete', 'partial') AND api_estimate_microusd IS NOT NULL AND
         api_estimate_microusd BETWEEN 0 AND 18446744073709551615 AND pricing_key IS NOT NULL)
    ),
    CHECK (api_estimate_state <> 'complete' OR unpriced_tokens = 0)
);

CREATE INDEX usage_buckets_period ON usage_buckets(bucket_start, device_id);
CREATE INDEX usage_buckets_model ON usage_buckets(canonical_model_id, observed_model_id, bucket_start);
-- Startup repricing finds rows derived by an older revision through this index.
CREATE INDEX usage_buckets_pricing_revision ON usage_buckets(pricing_revision);

COMMENT ON TABLE usage_buckets IS 'Privacy-limited absolute device buckets: client-observed evidence plus server-derived model and estimate. Raw observations and local source identities are excluded.';
COMMENT ON COLUMN usage_buckets.model_evidence IS 'How the source attributed the model. Evidence quality, not a catalog result.';
COMMENT ON COLUMN usage_buckets.payload_hash IS 'Digest of client evidence only; derived columns never cause a revision conflict.';
COMMENT ON COLUMN usage_buckets.pricing_revision IS 'Catalog revision that derived this row, including unknown models and unavailable estimates.';

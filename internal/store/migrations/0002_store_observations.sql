CREATE TABLE observations (
    identity TEXT PRIMARY KEY,
    source TEXT NOT NULL CHECK (source IN ('claude', 'codex')),
    surface TEXT NOT NULL CHECK (surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork', 'desktop', 'shared_local')),
    source_version TEXT NOT NULL CHECK (source_version <> ''),
    session_fingerprint TEXT,
    occurred_at TEXT NOT NULL,
    observed_model_id TEXT NOT NULL,
    canonical_model_id TEXT,
    model_provider TEXT,
    pricing_key TEXT,
    model_resolution TEXT NOT NULL CHECK (model_resolution IN ('reported', 'resolved', 'inferred', 'unknown')),
    input_uncached_tokens TEXT CHECK (input_uncached_tokens IS NULL OR (length(input_uncached_tokens) > 0 AND input_uncached_tokens NOT GLOB '*[^0-9]*')),
    input_cache_read_tokens TEXT CHECK (input_cache_read_tokens IS NULL OR (length(input_cache_read_tokens) > 0 AND input_cache_read_tokens NOT GLOB '*[^0-9]*')),
    input_cache_write_tokens TEXT CHECK (input_cache_write_tokens IS NULL OR (length(input_cache_write_tokens) > 0 AND input_cache_write_tokens NOT GLOB '*[^0-9]*')),
    output_tokens TEXT CHECK (output_tokens IS NULL OR (length(output_tokens) > 0 AND output_tokens NOT GLOB '*[^0-9]*')),
    reasoning_output_tokens TEXT CHECK (reasoning_output_tokens IS NULL OR (length(reasoning_output_tokens) > 0 AND reasoning_output_tokens NOT GLOB '*[^0-9]*')),
    count_semantics TEXT NOT NULL CHECK (count_semantics IN ('incremental', 'cumulative_snapshot')),
    measurement TEXT NOT NULL CHECK (measurement IN ('reported', 'derived', 'estimated')),
    adapter_version TEXT NOT NULL CHECK (adapter_version <> ''),
    normalization_version TEXT NOT NULL CHECK (normalization_version <> ''),
    warnings_json TEXT NOT NULL CHECK (json_valid(warnings_json))
	CHECK (
		(source = 'claude' AND surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork')) OR
		(source = 'codex' AND surface IN ('cli', 'desktop', 'shared_local'))
	)
) STRICT;

CREATE INDEX observations_occurred_at ON observations(occurred_at, identity);
CREATE INDEX observations_model ON observations(canonical_model_id, observed_model_id);

CREATE TABLE source_checkpoints (
    source_identity TEXT PRIMARY KEY,
    source TEXT NOT NULL CHECK (source IN ('claude', 'codex')),
    surface TEXT NOT NULL CHECK (surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork', 'desktop', 'shared_local')),
    cursor_value TEXT NOT NULL CHECK (length(cursor_value) > 0 AND cursor_value NOT GLOB '*[^0-9]*'),
    last_record_fingerprint TEXT,
    collected_at TEXT NOT NULL,
	CHECK (
		(source = 'claude' AND surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork')) OR
		(source = 'codex' AND surface IN ('cli', 'desktop', 'shared_local'))
	)
) STRICT;

CREATE TABLE adapter_health (
    source_identity TEXT PRIMARY KEY REFERENCES source_checkpoints(source_identity) ON DELETE CASCADE,
    source TEXT NOT NULL CHECK (source IN ('claude', 'codex')),
    surface TEXT NOT NULL CHECK (surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork', 'desktop', 'shared_local')),
    status TEXT NOT NULL CHECK (status IN ('detected', 'not_found', 'permission_denied', 'unsupported', 'degraded', 'error')),
    record_count TEXT CHECK (record_count IS NULL OR (length(record_count) > 0 AND record_count NOT GLOB '*[^0-9]*')),
    format TEXT NOT NULL,
    capabilities_json TEXT NOT NULL CHECK (json_valid(capabilities_json)),
    warnings_json TEXT NOT NULL CHECK (json_valid(warnings_json)),
    adapter_version TEXT NOT NULL CHECK (adapter_version <> ''),
    checked_at TEXT NOT NULL,
    last_success_at TEXT,
	CHECK (
		(source = 'claude' AND surface IN ('cli', 'desktop_code', 'desktop_agent', 'desktop_cowork')) OR
		(source = 'codex' AND surface IN ('cli', 'desktop', 'shared_local'))
	)
) STRICT;

-- Cache-write lifetimes split the combined total when a source reports them.
-- NULL means the time-to-live is unknown, never that nothing was written.
ALTER TABLE observations ADD COLUMN input_cache_write_5m_tokens TEXT
    CHECK (input_cache_write_5m_tokens IS NULL OR (length(input_cache_write_5m_tokens) > 0 AND input_cache_write_5m_tokens NOT GLOB '*[^0-9]*'));
ALTER TABLE observations ADD COLUMN input_cache_write_1h_tokens TEXT
    CHECK (input_cache_write_1h_tokens IS NULL OR (length(input_cache_write_1h_tokens) > 0 AND input_cache_write_1h_tokens NOT GLOB '*[^0-9]*'));

-- Earlier adapter revisions stored Claude's locally generated "<synthetic>"
-- assistant lines as usage. They never reached a model, and the adapter now
-- skips them, so a rescan would not replace these rows.
DELETE FROM usage_buckets WHERE source = 'claude' AND observed_model_id = '<synthetic>';
DELETE FROM observations WHERE source = 'claude' AND observed_model_id = '<synthetic>';

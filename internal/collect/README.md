# Collection semantics

The `collect` package owns the application scan boundary. Its orchestrator asks
the discovery and storage interfaces for work, while the collector opens
supported JSONL containers, delegates provider parsing to adapters, deduplicates
raw observations, converts cumulative counters to increments, and produces
deterministic normalized state.

Collection coordinates discovery and persistence through consuming-boundary
interfaces. It does not define provider roots, SQL behavior, uploads, or UI.

## Processing order

```text
candidate files
    -> source parser
    -> raw normalized observations
    -> deduplicate source identities
    -> reduce cumulative snapshots per session
    -> deterministic observations and warnings
    -> atomic observations + checkpoints + adapter health
```

Deduplication must happen before cumulative reduction. Reducing two overlapping copies first would turn the same snapshot stream into separate deltas and inflate totals.

## Duplicate conflicts

The source plus stable source-record identifier forms the duplicate key. Exact duplicates collapse silently. Conflicting copies emit `duplicate_conflict` and use a deterministic winner selected by:

1. number of known token categories;
2. non-overlapping token magnitude;
3. later source timestamp;
4. stable surface precedence;
5. a privacy-safe signature containing only normalized metadata.

The final tie-breaker never includes raw record content or a source path.

## Scan transactions and cancellation

All source batches from one scan commit in a single store transaction. A
partial parser failure degrades only the matching source health and does not
discard valid observations from other sources. If cancellation arrives after
discovery, already-normalized state is committed through a bounded detached
context before `Scan` returns the cancellation; this prevents a retry from
losing durable progress or inflating totals. The canceled state is recorded in
adapter health and returned to the caller.

## Cumulative counters

Cumulative state is isolated by source and local session identity. If a session identifier is unavailable, the adapter supplies a logical-file identity so unrelated files do not share state.

A decreased known counter starts a new segment and emits `counter_reset`; the current snapshot becomes the first increment in that segment. A missing category remains unknown for that observation, but the reducer remembers the last known value so a later reappearance is compared with the correct snapshot.

## Summaries

Summaries group by source, observed model identifier, and resolution quality. Reported and inferred usage for the same model string must remain distinct. If either side of a category is unknown, the summarized category remains unknown instead of presenting a partial sum as complete.

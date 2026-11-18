# Server boundary

The server is intentionally not implemented yet. This directory reserves the future Go API entry point described in the [architecture](../docs/architecture.md) and [roadmap](../ROADMAP.md).

The server will receive authenticated, privacy-limited aggregate buckets only after local normalization and persistence are complete. It must never accept prompts, responses, source code, command text, local paths, project or repository metadata, provider credentials, or raw source records.

Planned server responsibilities include device credentials, private groups, idempotent aggregate ingestion, pricing revisions, and leaderboard queries. Local discovery, provider parsing, cumulative-counter reduction, and raw-record deduplication remain native-client responsibilities.

Do not add distributed infrastructure, background queues, caches, or deployment configuration before a measured requirement and the corresponding roadmap stage.

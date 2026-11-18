# Synchronization

This package is the native boundary between committed local aggregates and the
hosted API. It never reads source records or normalized observations. Its input
is the SQLite outbox, whose payload already contains only absolute aggregate
dimensions and values.

The wire contract is an explicit allowlist with a version number and at most
100 buckets per request. Real upload and dry run use `BuildBatch`; dry run emits
the exact JSON body without contacting the network. The client retries a
transient failure once with backoff and jitter, validates every server
acknowledgement, and acknowledges only the exact local revision sent.

The server treats the device credential as ownership. User IDs, device IDs,
paths, source-record identities, sessions, prompts, responses, code, commands,
repository metadata, and provider credentials are not serialized in a batch.

# Architecture

Status: local native MVP implemented; network architecture remains planned
Last updated: November 18, 2026

## System goals

Overtok must:

- collect supported coding-agent usage without intercepting model traffic;
- preserve the difference between observed and inferred metadata;
- remain useful locally before login;
- aggregate multiple devices into one account;
- retry collection and synchronization without inflating totals;
- expose private-group leaderboards without uploading source content;
- stay simple enough to operate for a small beta.

## Repository shape

The repository uses the following current and planned boundaries:

```text
cmd/
  overtok/                 Native CLI entry point
internal/
  adapters/                Source-specific discovery and parsing
  application/             Local refresh and dashboard orchestration
  collect/                 Deduplication, counter reduction, and summaries
  doctor/                  Privacy-safe source and storage diagnostics
  fixturecheck/            Fixture privacy enforcement
  modelcatalog/            Model resolution and pricing revisions
  normalize/               Canonical usage records
  store/                   Local SQLite lifecycle and migrations
  sync/                    Buckets, outbox, and client protocol (planned)
  tui/                     Bubble Tea application shell and local views
server/
  cmd/api/                 Reserved HTTP API entry point
migrations/                Reserved database migrations
fixtures/                  Sanitized compatibility fixtures
packages/                  npm-distribution workspace (planned)
docs/                      Architecture and product design
```

The root, `docs/`, and `internal/` READMEs provide navigation. Complex implemented subsystems also document their local contracts beside the code.

## Local data flow

The implemented local collection path is:

```text
Narrow read-only source discovery
    -> provider JSONL parser or pinned ccusage contract
    -> validated normalized observations
    -> raw source-identity deduplication
    -> per-session cumulative-counter reduction
    -> one SQLite transaction for observations, checkpoints, and adapter health
```

Persistence follows this transaction flow:

```text
Normalized observations
    -> model resolution
    -> observation transaction
         - observation
         - source checkpoint
         - adapter health
    -> read committed observations
    -> aggregate transaction
         - absolute usage bucket revision
         - matching outbox revision
```

Collection never depends on the server. The TUI reads derived local state
through an application interface and does not parse source files or query
SQLite directly.

### Local application boundary

The application service is the only bridge used by the interactive TUI. A
manual refresh runs discovery, parsing, normalization, and the atomic
observation commit before rebuilding absolute usage buckets from committed
observations. Presentation retains its last stable page while that work runs.

Dashboard loads query Today, This week, 30 days, and All time using the local
timezone, then attach the privacy-safe database diagnostic snapshot. The TUI
receives no source paths, record identities, observation rows, or raw provider
content. Startup and refresh failures are reduced to stable messages and refer
users to `overtok doctor` for path-redacted detail.

### Source adapters

An adapter owns:

- source installation and data-root discovery;
- supported record versions;
- incremental versus cumulative counter semantics;
- stable source record identity;
- token-category interpretation;
- observed model extraction;
- source capability and health reporting.

Adapters emit observations; they do not calculate leaderboard ranks or perform uploads.

The implemented adapters parse supported Claude and Codex JSONL natively. They recover from malformed complete records, defer actively written final lines, preserve nullable categories, and emit stable local identities.

The optional ccusage integration invokes version 20.0.51 with pinned offline daily JSON commands. Overtok rejects any other version, unknown or duplicate fields, invalid counter relationships, and inconsistent totals before normalization. It never downloads the binary at runtime.

Claude Desktop discovery is limited to Code sessions and local Agent/Cowork evidence. Ordinary Claude Desktop chat storage is never scanned. Default Codex storage is classified as shared local evidence when CLI and Desktop attribution cannot be proven.

### Normalization

The canonical layer separates:

- the model string recorded by the source;
- the canonical model identity;
- provider identity;
- the pricing lookup key;
- the quality of each resolution.

It also converts source-specific token semantics into non-overlapping billable categories where possible. Unknown categories remain nullable. Reasoning tokens recorded as part of output remain subset metadata.

### Local persistence

SQLite runs in WAL mode and stores:

- adapter/source checkpoints;
- normalized observations;
- current usage bucket revisions;
- pending and acknowledged outbox records;
- model/pricing revisions;
- adapter health and last-success state.

Observation and checkpoint writes share a transaction. A source checkpoint cannot advance without the matching normalized state being durable.

Stored observation, source-root, session, and checkpoint identities are
one-way fingerprints. Raw filesystem paths and provider session identifiers do
not have schema columns. Nullable token categories use decimal text to preserve
both unknown values and the complete unsigned 64-bit range.

The local database uses the CGO-free `github.com/ncruces/go-sqlite3` driver and
lives under the platform user-configuration directory by default. Embedded,
checksummed migrations are contiguous and run as one immediate transaction.
Owner-only directory and file permissions are established before SQLite opens
the database. Connection-local foreign-key and busy-timeout settings are
applied to every pooled connection.

Local diagnostics read only allowlisted operational metadata from storage:
integrity and schema compatibility, collection freshness, adapter health,
checkpoint count, and pending-outbox count. The renderer distinguishes the
default and a custom database location without revealing its path. Diagnostic
checks never render observations, source identities, or database contents.

### Local usage buckets

Local bucket keys include the UTC interval, source, surface, observed and
canonical model identity, resolution quality, measurement quality, and
normalization version. A later upload envelope will add account and device
identity without changing the local absolute value.

Each local change increments the bucket revision. A newer local revision
replaces its earlier absolute value and the outbox retains only that newest
pending payload. Replaying a revision is therefore safe. No network client is
implemented at this stage.

Local buckets use one-minute UTC intervals. This preserves exact modern IANA
timezone day and week boundaries while grouping events that occur in the same
minute. Today starts at local midnight, weeks start Monday, and the 30-day view
uses 30 local calendar dates rather than a rolling 720-hour duration. Token and
price coverage remains complete, partial, or unavailable through every query.

## Server data flow

```text
GitHub device authorization
    -> Overtok user
    -> revocable device credential

Signed-in client
    -> bounded HTTPS batch
    -> authentication and schema validation
    -> idempotent bucket upsert
    -> acknowledgement

TUI
    -> authorized group query
    -> aggregate account devices
    -> rank by period and metric
```

### Identity and devices

The leaderboard identity is the user account. Devices only collect and upload.

- One GitHub identity maps to one Overtok user.
- One user can register multiple devices.
- Each device has a separate revocable credential.
- Memberships and invites reference users, not devices.
- Revoking a device prevents future writes without deleting other device data.

### Groups

The MVP supports private groups with owner and member roles. Invite codes are expiring, usage-limited authorization artifacts, not permanent credentials.

Leaderboard authorization checks membership on every request. A member of one group cannot enumerate another group or its users.

### Pricing

Pricing is a versioned registry derived from official provider model catalogs
plus small reviewed overrides. The local registry records source URLs,
effective or retrieval dates, and its applied revision. It provides standard
API-equivalent estimates only; it does not claim subscription usage was billed.

Resolution uses exact identifiers and reviewed mappings. Ambiguous routing
aliases remain unknown instead of selecting a backend model. Models with
context-dependent prices remain resolved but unpriced when local evidence does
not prove the applicable tier.

Unknown models retain token totals and display unavailable cost. Historical values do not change silently when the upstream catalog changes.

## Deployment

The beta should use one Go API deployment and one PostgreSQL database. Terraform manages infrastructure only after M0 and local correctness are complete.

The MVP does not require:

- WebSockets;
- Redis;
- queues;
- separate workers;
- a load balancer;
- horizontal scaling.

Polling on TUI entry and explicit refresh is sufficient. Add infrastructure only after measurement identifies a bottleneck or reliability requirement.

## Distribution architecture

The core application is a native Go binary. pnpm manages npm package metadata and publishing:

```text
@overtok/cli
    -> small platform selector
    -> @overtok/darwin-arm64
       @overtok/darwin-x64
       future Linux/Windows packages
```

The user still runs `npx @overtok/cli`. Release automation cross-compiles the Go binary, verifies checksums, publishes platform packages, and finally publishes the umbrella package.

Autosync points to a stable installed binary, not an ephemeral package-runner cache.

## Security and privacy invariants

- Provider credentials never leave their provider-owned storage.
- Raw source records never leave the machine.
- Upload serialization uses an allowlist, not field omission from a raw record.
- Logs avoid payload bodies, secrets, and source paths.
- Device credentials are stored using the best available OS credential store.
- Deletion and device revocation are first-class API operations.
- Client data is self-reported and never presented as cheat-proof.

## Feasibility decisions

M0 established these local rules:

- Pinned ccusage daily rows receive deterministic identities from schema, source, surface, UTC day, and observed model.
- Claude Desktop coding roots are discovered narrowly and parsed through native compatible-record adapters.
- Default Codex CLI and Desktop evidence is represented once as `shared_local`; a distinct `CODEX_HOME` permits separate CLI and Desktop roots.
- Cross-root deduplication happens before cumulative snapshots become deltas.
- Source capabilities distinguish reported, derived, inferred, and unsupported fields.
- Missing model or token metadata remains unknown rather than becoming zero or a guessed alias.

The local interval is fixed at one minute. A later network protocol may define
a coarser upload projection, but it must retain absolute replacement revisions
and the same privacy allowlist.

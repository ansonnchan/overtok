# Architecture

Status: implemented local-collection boundaries plus target MVP architecture
Last updated: November 17, 2026

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
  collect/                 Deduplication, counter reduction, and summaries
  doctor/                  Privacy-safe source diagnostics
  fixturecheck/            Fixture privacy enforcement
  modelcatalog/            Model resolution and pricing revisions (planned)
  normalize/               Canonical usage records
  store/                   Local SQLite lifecycle and migrations
  sync/                    Buckets, outbox, and client protocol (planned)
  tui/                     Bubble Tea models and views (planned)
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
    -> SQLite transaction
         - observation
         - source checkpoint
         - revised usage bucket
         - outbox revision
```

Collection never depends on the server. The future TUI reads derived local state and does not parse source files directly.

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

### Upload buckets (planned)

The client uploads absolute bucket snapshots rather than additive deltas. A bucket key includes the account, device, time interval, source, surface, model identity, measurement quality, and schema version.

Each local change increments the bucket revision. The server accepts a revision only when it is newer than the stored revision. Replaying a batch is therefore safe.

The exact interval will be selected with synchronization work. It must balance timezone accuracy, privacy, and row count.

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

Pricing is a versioned registry derived from public model catalogs plus small reviewed overrides. The server prices leaderboard data consistently and records the applied revision.

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

The synchronization bucket interval remains intentionally open until the persistence and protocol stages can evaluate timezone accuracy, privacy, and row volume together.

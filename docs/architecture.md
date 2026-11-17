# Architecture

Status: target architecture for the MVP
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

The intended monorepo layout is:

```text
cmd/
  overtok/                 Native CLI entry point
internal/
  adapters/                Source-specific discovery and parsing
  collect/                 Collection orchestration and checkpoints
  modelcatalog/            Model resolution and pricing revisions
  normalize/               Canonical usage records
  store/                   Local SQLite persistence
  sync/                    Buckets, outbox, and client protocol
  tui/                     Bubble Tea models and views
server/
  cmd/api/                 HTTP API entry point
  internal/auth/           GitHub identity and device credentials
  internal/groups/         Groups, memberships, and invites
  internal/ingest/         Idempotent usage ingestion
  internal/leaderboard/    Period and metric queries
migrations/                PostgreSQL migrations
fixtures/                  Sanitized compatibility fixtures
packages/                  pnpm npm-distribution workspace
docs/                      Architecture and product design
```

Package names may evolve during OVT-001, but the boundaries should remain.

## Local data flow

```text
Source discovery
    -> adapter parse
    -> source-specific observations
    -> canonical normalization
    -> model resolution
    -> SQLite transaction
         - observation
         - source checkpoint
         - revised usage bucket
         - outbox revision
```

Collection never depends on the server. The TUI reads derived local state and does not parse source files directly.

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

The initial integration may invoke a pinned ccusage native executable with JSON output. Overtok validates that output and converts it into its own contract. Claude Desktop local-agent discovery may wrap compatible roots or require a focused Go adapter.

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

### Upload buckets

The client uploads absolute bucket snapshots rather than additive deltas. A bucket key includes the account, device, time interval, source, surface, model identity, measurement quality, and schema version.

Each local change increments the bucket revision. The server accepts a revision only when it is newer than the stored revision. Replaying a batch is therefore safe.

The exact interval will be selected during the feasibility phase. It must balance timezone accuracy, privacy, and row count.

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

## Open feasibility decisions

M0 must resolve:

- whether pinned ccusage output contains sufficient stable identity for local deduplication;
- whether Claude Desktop roots can be parsed through explicit ccusage paths;
- whether Codex Desktop duplicates CLI-visible rollouts;
- which bucket interval preserves period semantics without excessive behavioral detail;
- which fields are reliably available across supported source versions.

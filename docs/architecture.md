# Architecture

Status: M3 implementation complete; real-machine journey validation pending
Last updated: November 18, 2026

## System goals

tokenholic must:

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
  tokenholic/                 Native CLI entry point
internal/
  account/                 Native login and OS credential-vault storage
  adapters/                Source-specific discovery and parsing
  application/             Local refresh and dashboard orchestration
  collect/                 Deduplication and counter reduction
  doctor/                  Privacy-safe source and storage diagnostics
  fixturecheck/            Fixture privacy enforcement
  modelcatalog/            Model resolution and pricing revisions
  normalize/               Canonical usage records
  store/                   Local SQLite lifecycle and migrations
  sync/                    Allowlisted batches, retry, dry run, and acknowledgements
  tui/                     Bubble Tea application shell and local views
server/
  account/                 Authorized multi-device account aggregation
  auth/                    GitHub device flow and tokenholic device credentials
  cmd/api/                 Hosted HTTP API entry point
  httpapi/                 Bounded JSON routing and protocol errors
  postgres/                Hosted connection and migration boundary
  sync/                    Authenticated absolute-revision application
migrations/                Embedded hosted PostgreSQL schema history
fixtures/                  Sanitized compatibility fixtures
packages/                  npm-distribution workspace (planned)
docs/                      Architecture and product design
```

The root, `docs/`, and `internal/` READMEs provide navigation. Complex implemented subsystems also document their local contracts beside the code.

## Local data flow

The implemented local collection path is:

```text
Narrow read-only source discovery
    -> provider JSONL parser
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
users to `tokenholic doctor` for path-redacted detail.

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

The retained ccusage compatibility contract pins version 20.0.51 and its
offline daily JSON commands. Release tooling validates the manifest, diagnostics
can probe an installed version, and CI validates synthetic report fixtures.
Native collection does not invoke or download ccusage.

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
pending payload. Replaying a revision is therefore safe. The sync client reads
only this committed outbox, sends at most 100 allowlisted buckets per request,
and marks only the exact acknowledged revision as durable remotely. A late
acknowledgement cannot clear a newer local replacement.

Both real sync and dry run collect, normalize, and commit locally before reading
the outbox. Dry run writes the exact request JSON produced by the real serializer
and performs no network or acknowledgement mutation. Transient network, rate
limit, and server failures receive one exponential-backoff retry with jitter;
an interrupted or rejected batch remains pending.

Local buckets use one-minute UTC intervals. This preserves exact modern IANA
timezone day and week boundaries while grouping events that occur in the same
minute. Today starts at local midnight, weeks start Monday, and the 30-day view
uses 30 local calendar dates rather than a rolling 720-hour duration. Token and
price coverage remains complete, partial, or unavailable through every query.

## Server data flow

The hosted persistence, device authentication, and synchronization foundations are implemented. PostgreSQL stores users,
GitHub identities, independently revocable devices and credential hashes,
pricing revisions, and privacy-limited absolute usage buckets. The HTTP API
implements GitHub device login, credential rotation, device revocation, and
bounded authenticated bucket upload.

Server migrations are embedded in the Go binary, contiguous, checksummed, and
applied once inside an advisory-locked transaction. Applied migration history
must match the binary exactly; changed or newer history is rejected instead of
being silently repaired. The current implementation targets PostgreSQL 18 and
uses pgx for connection pooling and migration execution.

The schema owns bucket identity with `(device_id, bucket_key)`. Equal revisions
with the same canonical client-evidence payload are no-ops, older revisions are
acknowledged as stale, and newer absolute revisions replace earlier values.
Server-derived price and pricing-revision fields are excluded from conflict
identity so a trusted catalog update cannot turn an otherwise identical retry
into a conflict. Other equal-revision differences reject the transaction.
Foreign keys make user ownership explicit and cascade user deletion through
devices, credential hashes, and usage buckets. The schema has no columns for
raw source records, local identities, paths, prompts, responses, code, project
metadata, commands, or provider credentials.

The server registers only trusted bundled model and pricing revisions. Upload
serialization is an explicit field allowlist and stores normalized columns plus
a canonical payload hash, never the client JSON body or local observation IDs.
Clients report locally observed token counts and evidence quality, but the API
rejects model metadata that disagrees with its catalog and replaces every
client-provided API-equivalent estimate and pricing revision with a value
derived from its trusted registry. Missing token categories or prices remain
unavailable; partial token coverage produces a partial lower bound.

Account usage is read as one repeatable PostgreSQL snapshot. Normalized token
categories, non-overlapping total tokens, and API-equivalent estimates preserve
complete, partial, unavailable, and empty coverage. Numeric sums remain decimal
strings so multiple devices cannot overflow a native unsigned integer.

Only active devices contribute to the account total. The response includes all
account devices with revocation, last-authenticated, and last-synchronized
state. Revocation blocks subsequent writes and removes that device from active
totals without deleting its synchronized history or affecting sibling devices.
Authenticated account deletion hard-deletes the user row. Ownership foreign
keys erase GitHub identity, devices, credential hashes, synchronized usage,
owned groups, invitations, and memberships in the same transaction while
leaving unrelated accounts and shared pricing revisions intact. The native
client removes its OS-vault session only after the API confirms deletion;
local SQLite usage remains on each machine.

```text
GitHub device authorization
    -> tokenholic user
    -> revocable device credential

Signed-in client
    -> bounded HTTPS batch
    -> authentication, schema, and model-catalog validation
    -> server-derived API-equivalent estimate
    -> idempotent bucket upsert
    -> acknowledgement

TUI
    -> authorized group query
    -> aggregate account devices
    -> rank by period and metric
```

The native `multiplayer` boundary owns authenticated group HTTP calls and reads
the existing device session from the operating-system credential vault. The
TUI consumes its presentation-safe snapshots; it does not own credentials,
HTTP, group mutation policy, or leaderboard aggregation. With no API origin,
the local TUI remains fully usable and the Groups area explains how to opt in.

### Identity and devices

The leaderboard identity is the user account. Devices only collect and upload.

- One GitHub identity maps to one tokenholic user.
- One user can register multiple devices.
- Each device has a separate revocable credential.
- Memberships and invites reference users, not devices.
- Revoking a device prevents future writes without deleting other device data.

The CLI begins authorization through the tokenholic API and displays GitHub's
verification URL and user code. Short-lived GitHub device state is encrypted
and authenticated inside an opaque flow token. After approval, the API uses the
GitHub access token only in memory to read the stable numeric ID and login, then
discards it. Neither PostgreSQL nor the native client stores that token.

tokenholic device credentials contain a public credential ID and 256-bit random
secret. PostgreSQL stores only the SHA-256 secret hash. Rotation revokes the old
credential atomically, logout revokes the entire device, and other devices on
the same account remain independent. The native client stores its session in
the operating-system credential vault with no plaintext-file fallback.

### Groups

The MVP supports private groups with owner and member roles. Invite codes are
expiring, usage-limited authorization artifacts, not permanent credentials.
Only invite hashes are persisted. Group reads, invitation creation, joining,
leaving, and member revocation authorize active membership inside the same
repository boundary that performs the operation. Non-members receive the same
not-found result whether a group exists or not.

Leaderboard authorization checks membership on every request. A member of one
group cannot enumerate another group or its users.

### Leaderboards

One repeatable-read snapshot aggregates every active member's active devices.
The query calculates Today, current week, and 30-local-date boundaries from the
group's IANA timezone; all periods end at the response generation time. Weeks
start Monday. All-time has no lower bound.

Rows rank by complete total tokens, output tokens, or API-equivalent estimates.
Partial lower-bound values remain visible but unranked, while unavailable and
empty values remain distinct from zero. Equal complete values use competition
ranking. Every row includes last-upload freshness, source mix, most-used model,
and counts for unknown or inferred model resolution and unavailable API
estimates. Revoked devices and inactive memberships do not contribute.

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

The optional demo environment uses one stateless Go API deployment on Render
and one Supabase PostgreSQL database. A root Blueprint and a production
container make that environment reproducible without introducing application
dependencies on either provider. Local collection remains the primary product
and does not require the hosted service.

The free environment exists for end-to-end personal testing and interviews,
not for a production availability or durability claim. Free-tier sleeping,
pausing, and missing automatic backups are accepted constraints. If measured
usage later justifies a persistent beta, infrastructure-as-code, managed
backups, budgets, and alerts should be evaluated as a separate operational
milestone rather than implied by the demo configuration.

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
@tokenholic/cli
    -> small platform selector
    -> @tokenholic/darwin-arm64
       @tokenholic/darwin-x64
       future Linux/Windows packages
```

The user still runs `npx @tokenholic/cli`. Release automation cross-compiles the
Go binary without CGO, verifies its Mach-O architecture and embedded version,
normalizes archive metadata, and publishes checksummed native artifacts. Each
native archive includes the pinned ccusage contract but never downloads or
bundles an unverified parser executable. Platform packages consume these
artifacts, and the umbrella npm package is published only after both platform
packages exist.
The launcher resolves only the package matching the current operating system
and CPU, then executes its binary directly without a shell. Unsupported targets
and missing optional packages fail explicitly.

Autosync copies the current native executable to a stable private
application-support path only after explicit user consent. A per-user launch
agent runs it at login and hourly, stores no credentials, and writes only the
sync command's privacy-safe summary or stable failure message to private local
logs. Manual and scheduled synchronization share a non-blocking advisory lock,
so they cannot overlap. Disabling autosync unloads the agent and removes its
binary, plist, and logs without touching SQLite usage or the OS-vault session.

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

- ccusage remains a pinned compatibility contract, not an active collection path.
- Claude Desktop coding roots are discovered narrowly and parsed through native compatible-record adapters.
- Default Codex CLI and Desktop evidence is represented once as `shared_local`; a distinct `CODEX_HOME` permits separate CLI and Desktop roots.
- Cross-root deduplication happens before cumulative snapshots become deltas.
- Source capabilities distinguish reported, derived, inferred, and unsupported fields.
- Missing model or token metadata remains unknown rather than becoming zero or a guessed alias.

The local interval is fixed at one minute. A later network protocol may define
a coarser upload projection, but it must retain absolute replacement revisions
and the same privacy allowlist.

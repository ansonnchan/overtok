# Overtok roadmap

Status: native release artifacts implemented; npm distribution next
Last updated: November 18, 2026

## Product definition

Overtok is a terminal-native, multiplayer leaderboard for coding-agent usage. It reads locally available usage records, normalizes model and token metadata, and synchronizes privacy-limited aggregates to private groups.

The product tracks coding-agent activity only. Ordinary Claude Desktop chat, claude.ai, general chat applications, prompts, responses, source code, file paths, and project names are outside the MVP.

### MVP user journey

```text
npx @overtok/cli
  -> detect local Claude Code and Codex usage
  -> show personal totals in a TUI
  -> authenticate with GitHub device flow
  -> create or join a private group
  -> synchronize usage
  -> view an account-level leaderboard across all devices
  -> optionally enable hourly autosync
```

### MVP commands

```text
overtok                         # Open the TUI
overtok login                   # GitHub device authentication
overtok logout
overtok sync                    # Collect locally and upload pending aggregates
overtok sync --dry-run          # Show what would be uploaded
overtok group create <name>
overtok group join <code>
overtok group leave <name>
overtok autosync enable         # Install an hourly OS scheduler
overtok autosync disable
overtok autosync status
overtok doctor                  # Diagnose sources, auth, storage, and sync
```

## Scope decisions

### Supported in the MVP

- macOS on Apple Silicon and Intel.
- Claude Code CLI sessions.
- Claude Desktop local Code/Agent/Cowork sessions when compatible local usage records are present.
- Codex CLI and Codex Desktop sessions.
- Exact observed model IDs where the source reports them.
- Explicit `reported`, `resolved`, `inferred`, and `unknown` model-resolution states.
- Input, output, cache-read, and cache-write categories when the source exposes them.
- GitHub account identity with multiple independently revocable devices.
- Private invite-only groups.
- Today, current week, last 30 days, and all-time leaderboards.
- Total tokens, output tokens, and API-equivalent cost views.
- Optional hourly background synchronization.

### Explicitly excluded from the MVP

- Ordinary Claude Desktop chat, claude.ai, ChatGPT chat, or mobile chat usage.
- Cursor until reliable local token and model evidence is validated.
- Public or global leaderboards.
- Browser dashboard.
- Writing-versus-reading-versus-discussion classification.
- Productivity scoring, code-quality scoring, or claims that more tokens mean better work.
- Real-time WebSockets, Redis, queues, or horizontal scaling.
- Windows support.
- Cryptographic proof that client-reported usage is genuine.

## Model and source strategy

Models and sources are different extension points:

- A **source adapter** knows where a coding client stores usage and how its records behave.
- A **model resolver** maps the model identifier emitted by a source to a canonical model and pricing entry.

A newly released model should continue to count tokens without an Overtok release when an existing source reports its identifier. Until pricing metadata is available, Overtok displays the observed model and token counts with cost marked as unavailable.

A newly supported coding client requires a source adapter or a validated ccusage integration.

### Initial source matrix

| Source | MVP status | Collection path | Notes |
| --- | --- | --- | --- |
| Claude Code CLI | P0 | Claude-compatible local JSONL | Report exact model and token categories when present |
| Claude Desktop Code/Agent/Cowork | P0 | Desktop local-agent session discovery plus Claude-compatible JSONL | Keep the desktop surface distinct from CLI; deduplicate shared records |
| Codex CLI | P0 | Codex rollout JSONL through a pinned parser | Respect cumulative counters and active `turn_context` model |
| Codex Desktop | P0 | Codex local session/rollout storage | Verify whether it shares or duplicates CLI roots |
| OpenCode | Extension | ccusage or native SQLite adapter | Add after MVP stability |
| Gemini CLI | Extension | ccusage or validated local telemetry | Add after MVP stability |
| Grok Build CLI | Extension | ccusage | Do not claim generic Grok chat support |
| Cursor | Research | Unknown | Ship only if model and token evidence is reliable |

## Target architecture

```text
Local coding-agent records
        |
        v
Source discovery and adapters
        |
        v
Normalized observations + model resolution
        |
        v
Local SQLite
  - source checkpoints
  - normalized observations
  - aggregate buckets
  - sync outbox
  - adapter health
        |
        +------------------> Bubble Tea TUI
        |
        v
Idempotent HTTPS sync
        |
        v
Go API + PostgreSQL
  - users and devices
  - groups, memberships, invites
  - usage buckets
  - pricing revisions
        |
        v
Leaderboard responses
```

### Local technology

- Go CLI and collector.
- Bubble Tea and Lip Gloss for the TUI.
- SQLite in WAL mode for checkpoints, observations, buckets, and the outbox.
- Pinned ccusage native executable for the first parser integration, invoked with structured JSON output.
- Native adapters only where ccusage does not discover the required surface, beginning with Claude Desktop local-agent sessions if needed.

### Server technology

- Go HTTP API.
- PostgreSQL for relational group and device state.
- Terraform-managed deployment after local collection semantics are proven.
- One API deployment and one database for the beta; no load balancer or distributed cache unless measurements justify them.

### Distribution

- `@overtok/cli` is the umbrella npm package and exposes the `overtok` executable.
- Platform-specific packages contain native Go binaries for supported OS/CPU combinations.
- A small launcher chooses the installed platform binary; users still run one command.
- GitHub Actions or GoReleaser builds, checksums, signs, and publishes releases.
- Homebrew is a post-MVP distribution channel.

## Data rules

### Normalized observation

The internal record must retain observed facts separately from resolved metadata:

```text
source
surface
source_version
source_record_id
session_id_local
occurred_at
observed_model_id
canonical_model_id
model_provider
model_resolution: reported | resolved | inferred | unknown
input_uncached_tokens
input_cache_read_tokens
input_cache_write_tokens
output_tokens
reasoning_output_tokens
count_semantics: incremental | cumulative_snapshot
measurement: reported | derived | estimated
adapter_version
normalization_version
warnings
```

Unknown values remain null rather than being converted to zero. Reasoning tokens that are a subset of output tokens must never be added twice.

### Synchronization

- Normalize and persist locally before attempting network upload.
- Convert observations into absolute, versioned UTC time buckets.
- Retry uploads at least once with exponential backoff and jitter.
- A repeated bucket revision is a no-op on the server.
- A newer revision replaces the older absolute bucket instead of adding a delta.
- Leaderboard rows aggregate all active devices belonging to the user account.
- Group membership belongs to the user, never to an individual device.
- Every device has a separate revocable credential.

### Privacy boundary

The default upload payload may include:

- user and device identifiers;
- UTC time bucket;
- source and surface;
- observed/canonical model identifiers and resolution quality;
- normalized token categories;
- schema and adapter versions.

It must not include:

- prompts or responses;
- source code or diffs;
- tool inputs or command text;
- file paths, project names, repository names, or branch names;
- provider credentials;
- raw session transcripts.

`overtok sync --dry-run` must use the real serialization path and show exactly what would be uploaded.

## Milestones and PR-sized workstreams

The identifiers below exist only for internal roadmap cross-references. Do not copy them into branch names, PR titles, commit subjects, or trailers. GitHub issues are optional; preserve the relevant acceptance criteria and dependencies in each focused PR description.

### M0 — Feasibility and foundations (complete)

Goal: prove that the required local evidence exists and freeze the internal contract before building cloud infrastructure.

- [x] **OVT-001: Initialize the Go workspace and repository layout**
  - Create CLI, internal packages, API placeholder, migrations, fixtures, and documentation directories.
  - Add formatting, linting, unit-test, and race-test commands.
  - Acceptance: a clean checkout builds and tests with one documented command.

- [x] **OVT-002: Define normalized observation and capability contracts**
  - Represent count semantics, model-resolution quality, nullable categories, adapter health, and warnings.
  - Acceptance: schema tests cover reported, inferred, unknown, cumulative, and incremental records.
  - Depends on: OVT-001.

- [x] **OVT-003: Create sanitized compatibility fixtures**
  - Capture representative Claude Code CLI, Claude Desktop local-agent, Codex CLI, and Codex Desktop records.
  - Remove prompts, responses, paths, identities, repository metadata, and credentials.
  - Acceptance: fixtures preserve token/model/counter behavior without private content.
  - Depends on: OVT-001.

- [x] **OVT-004: Spike pinned ccusage execution and JSON validation**
  - Resolve and execute a pinned ccusage binary.
  - Reject unsupported versions and malformed output safely.
  - Determine which required per-model and per-period fields are available.
  - Acceptance: Claude Code and Codex fixtures produce validated structured output in tests.
  - Depends on: OVT-002, OVT-003.

- [x] **OVT-005: Discover Claude Desktop local coding sessions**
  - Implement macOS path discovery for local Code/Agent/Cowork sessions.
  - Determine whether ccusage can parse explicit discovered roots or a small Go adapter is required.
  - Acceptance: a desktop fixture is discovered, classified as a desktop surface, and parsed without reading ordinary chat data.
  - Depends on: OVT-003, OVT-004.

- [x] **OVT-006: Verify cumulative and duplicate semantics**
  - Test rescans, partially written JSONL, cumulative Codex counters, repeated Claude request rows, and desktop/CLI overlap.
  - Acceptance: rescanning unchanged fixtures never increases normalized totals.
  - Depends on: OVT-004, OVT-005.

M0 exit criteria:

- Claude CLI, Claude Desktop coding sessions, and Codex yield attributable model/token records from sanitized fixtures.
- Unsupported or ambiguous records fail visibly instead of becoming zeroes or guessed models.
- Repeat parsing is deterministic and duplicate-safe.
- The normalized schema is stable enough to persist locally.

### M1 — Local native MVP

Goal: deliver a useful local application before authentication or multiplayer work.

- [x] **OVT-010: Implement local SQLite storage and migrations**
  - Add checkpoints, observations, buckets, outbox, and adapter-health tables.
  - Use transactions and WAL mode.
  - Acceptance: interruption and restart tests preserve committed observations and checkpoints.
  - Depends on: M0.

- [x] **OVT-011: Implement source collection orchestration**
  - Detect sources, collect incrementally, normalize, persist, and report adapter warnings.
  - Acceptance: one scan combines CLI and desktop sources without duplicates.
  - Depends on: OVT-010.

- [x] **OVT-012: Implement model resolution and pricing registry**
  - Store observed, canonical, provider, pricing key, quality, source, and revision separately.
  - Unknown models retain tokens while cost remains unavailable.
  - Acceptance: reported, aliased, routed, and unknown fixtures display correctly.
  - Depends on: OVT-002, OVT-010.

- [x] **OVT-013: Build the initial Bubble Tea TUI**
  - Overview, sources/health, models, recent periods, and settings/help views.
  - Support narrow terminals and graceful no-color output.
  - Acceptance: a new user can identify detected sources, totals, model mix, and collection warnings without reading documentation.
  - Depends on: OVT-011, OVT-012.

- [x] **OVT-014: Implement `overtok doctor`**
  - Report platform, source paths without sensitive details, parser version, database health, and collection errors.
  - Acceptance: missing sources, permissions, unsupported formats, and corrupt local state have actionable diagnostics.
  - Depends on: OVT-011.

M1 exit criteria:

- The native app is useful with no server configured.
- It shows model and token totals from Claude and Codex coding sessions.
- Closing or interrupting the app does not corrupt local state.
- Repeated scans do not inflate totals.

### M2 — Accounts, devices, and synchronization (complete)

Goal: establish one account across multiple independently managed devices.

- [x] **OVT-020: Define and migrate the server data model**
  - Users, GitHub identities, devices, device credentials, pricing revisions, and usage buckets.
  - Acceptance: database constraints enforce ownership and bucket idempotency.

- [x] **OVT-021: Implement GitHub device-flow login**
  - Complete CLI-friendly authorization without accepting a GitHub password.
  - Exchange GitHub identity for an Overtok account and device credential.
  - Acceptance: login, logout, credential refresh/failure, and device revocation are tested.
  - Depends on: OVT-020.

- [x] **OVT-022: Implement local outbox and sync protocol**
  - Build absolute versioned buckets, bounded batches, retry behavior, acknowledgements, and dry-run serialization.
  - Acceptance: retries, duplicate submissions, out-of-order revisions, and network interruption do not inflate totals.
  - Depends on: OVT-010, OVT-020, OVT-021.

- [x] **OVT-023: Implement account-level multi-device aggregation**
  - Aggregate devices under one user while retaining device revocation and freshness.
  - Acceptance: two devices contribute to one account row; revoking one prevents future writes without deleting the other.
  - Depends on: OVT-022.

M2 exit criteria:

- A user can sign in and synchronize from two devices.
- Retrying the same upload cannot change totals.
- Synced data matches the dry-run preview and contains no prohibited fields.

### M3 — Private groups and leaderboards

Goal: complete the social product loop.

- [x] **OVT-030: Implement groups, memberships, roles, and invites**
  - Private groups only, owner/member roles, expiring invite codes, usage limits, and leave/revoke behavior.
  - Acceptance: authorization tests prevent non-members from reading or mutating a group.

- [x] **OVT-031: Implement period and metric leaderboard queries**
  - Today, current week, last 30 days, and all-time in a group timezone.
  - Total tokens, output tokens, and API-equivalent cost.
  - Acceptance: query tests cover timezone boundaries, multiple devices, unknown prices, and ties.
  - Depends on: OVT-023, OVT-030.

- [x] **OVT-032: Add group and leaderboard views to the TUI**
  - Join/create/switch group, ranking, personal standing, source mix, most-used model, and freshness.
  - Acceptance: two users can join a group, sync independently, and see matching rankings.
  - Depends on: OVT-031.

- [x] **OVT-033: Implement account and uploaded-data deletion**
  - Revoke devices, leave groups, erase synchronized usage, and delete the account.
  - Acceptance: deletion is authorization-checked, complete, and tested.
  - Depends on: OVT-020, OVT-030.

M3 exit criteria:

- The full MVP user journey works on real machines for at least two users.
- Group authorization and deletion behavior have automated tests.
- Every leaderboard value exposes period, metric, freshness, and model-resolution limitations.

### M4 — Packaging, autosync, and deployment

Goal: make Overtok installable and reliable without developer assistance.

- [x] **OVT-040: Build cross-platform release artifacts**
  - Produce checksummed macOS arm64/x64 binaries and pinned parser assets.
  - Acceptance: artifacts run on clean supported machines.

- [ ] **OVT-041: Publish the npm umbrella and platform packages**
  - Expose one `overtok` executable through `npx @overtok/cli`.
  - Fail with an actionable message on unsupported platforms.
  - Acceptance: a clean macOS machine reaches the TUI with the documented one-liner.
  - Depends on: OVT-040.

- [ ] **OVT-042: Implement macOS autosync**
  - Install, inspect, and remove a user-level `launchd` schedule.
  - Use a stable executable path, a single-instance lock, and local logs without sensitive payloads.
  - Acceptance: hourly sync survives logout/restart, does not overlap manual sync, and is fully removable.
  - Depends on: OVT-022, OVT-040.

- [ ] **OVT-043: Provision the beta environment with Terraform**
  - Deploy the Go API, PostgreSQL, TLS, secrets, backups, logs, budgets, and alerts.
  - Acceptance: the environment can be created from documented inputs and restored from a tested backup.
  - Depends on: M3.

M4 exit criteria:

- A tester can install with one command, authenticate, join a group, sync, and enable autosync without repository access.
- Releases are reproducible and versioned.
- The beta environment has backups, cost limits, and minimum operational visibility.

### M5 — Private beta and hardening

Goal: validate reliability with real installations rather than chase scale.

- [ ] **OVT-050: Run a 5-user compatibility pilot**
  - Capture installation, provider detection, sync, and retention failures.
  - Fix blockers before expanding the cohort.

- [ ] **OVT-051: Expand to a 10–20 developer beta**
  - Prefer sustained weekly use over one-time installs.
  - Collect only operational product metrics; do not collect prompts or source metadata.
  - Depends on: OVT-050.

- [ ] **OVT-052: Publish compatibility and privacy documentation**
  - Document supported client versions, coverage limitations, upload fields, deletion, and the self-reported-data caveat.

- [ ] **OVT-053: Measure and harden the critical path**
  - Track parser failures, sync success, duplicate rejections, API latency, and stale devices.
  - Add regression fixtures for every real compatibility bug.

M5 success signals:

- At least 5 weekly active testers across four consecutive weeks.
- At least 2 active private groups.
- Repeat sync and retry incidents do not inflate totals.
- Real compatibility bugs become sanitized regression fixtures.
- Users can install and recover from common failures without direct developer intervention.

## Extensions after MVP

Extensions are gated by evidence, not by the number of tools in the market.

### Source extensions

1. Linux support and user-level `systemd` autosync.
2. OpenCode.
3. Gemini CLI.
4. Grok Build CLI.
5. Cursor only after reliable token/model evidence is verified.
6. Windows packaging and Task Scheduler support.

Each source must ship with:

- sanitized fixtures;
- discovery tests;
- incremental/cumulative semantics tests;
- model and token capability declarations;
- duplicate and partial-write tests;
- explicit unsupported-version behavior.

### Product extensions

- Configurable group seasons and reset rules.
- Team-versus-team views.
- Optional public groups.
- Self-hosted deployment documentation.
- Homebrew distribution.
- Personal quota status that remains separate from leaderboard token accounting.

### Deferred research

- Privacy-preserving cross-device deduplication when identical source logs are copied between devices.
- Signed client releases and provenance verification.
- Historical price correction migrations.
- A stable public adapter SDK.

## Project guardrails

- Do not add a source without trustworthy fixtures.
- Do not treat missing fields as zero.
- Do not infer a backend model silently from a routing alias.
- Do not send raw source records to the server.
- Do not install autosync without explicit consent.
- Do not call API-equivalent cost actual spend.
- Do not optimize for scale before measuring a real bottleneck.
- Do not publish adoption, latency, or reliability numbers that were not measured.

## Immediate starting sequence

M0 is complete. Begin M1 with local SQLite storage, then connect the proven discovery and normalization pipeline to transactional collection orchestration. Do not begin the API, authentication, groups, Terraform, or npm platform packages before the corresponding later milestone.

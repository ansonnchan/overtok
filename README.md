# Overtok

Overtok is a terminal-first application for understanding coding-agent usage.
The current local implementation collects supported Claude and Codex evidence,
normalizes token and model metadata, persists privacy-safe aggregates, reports
installation health, and provides a keyboard-first application shell without
reading ordinary chat data.

The hosted PostgreSQL data model, HTTP API, GitHub device login, idempotent
absolute-bucket synchronization, and account-level multi-device aggregation
complete the accounts/devices milestone. Groups and multiplayer leaderboards
remain later roadmap stages and are not required by local mode.

## Privacy boundary

Overtok is designed to count usage, not inspect work. Local collection must never upload or retain prompts, responses, source code, command text, credentials, identifying paths, project names, repository metadata, branch names, or raw transcripts.

The compatibility suite uses synthetic fixtures. Do not replace them with records copied from a real local agent installation. See [fixtures/README.md](./fixtures/README.md) before adding a source-format regression case.

## Current capabilities

The native Go command currently provides:

- read-only discovery for Claude Code CLI;
- read-only discovery for Claude Desktop Code, Agent, and Cowork sessions;
- read-only discovery for Codex CLI and Codex Desktop/shared local storage;
- native parsing of supported Claude and Codex JSONL evidence;
- strict validation of pinned ccusage Claude and Codex daily JSON;
- deterministic per-model normalization, cumulative-counter reduction, recovery from partial records, and deduplication;
- transactional local SQLite observations, checkpoints, health, and absolute usage buckets;
- timezone-aware local totals with explicit complete, partial, unavailable, and empty states;
- a keyboard-first Overview, Sources, Models, and Settings TUI with manual collection;
- GitHub device-flow `login` and revoking `logout` commands backed by the OS credential vault;
- bounded `sync` and exact-payload `sync --dry-run` commands with durable acknowledgements;
- one authenticated account usage row across active devices with independent revocation and freshness;
- private groups with owner/member roles, expiring limited-use invites, and membership-authorized reads; and
- privacy-safe source and storage diagnostics through `overtok doctor`.

Model names are not allowlisted. A previously unseen, non-empty observed identifier can retain its token counts while canonical resolution and pricing remain unavailable. Reported, resolved, inferred, and unknown attribution are separate states.

## Requirements

- Go 1.25 or newer.
- macOS for the currently implemented discovery paths.
- PostgreSQL 18 only when exercising the hosted schema integration tests.
- ccusage 20.0.51 only when exercising the optional ccusage integration. Overtok does not download it at runtime.

The pnpm workspace is reserved for later npm release tooling; it is not needed to build the native command today.

Account commands require an Overtok API origin:

```sh
OVERTOK_API_URL=https://api.example.test overtok login
OVERTOK_API_URL=https://api.example.test overtok logout
OVERTOK_API_URL=https://api.example.test overtok sync --dry-run
OVERTOK_API_URL=https://api.example.test overtok sync
```

Login requests public GitHub identity only. The CLI never receives a GitHub
access token, and the API uses it only in memory to resolve the stable numeric
GitHub identity before discarding it.

## Build and verify

Run the complete local verification suite from a clean checkout:

```sh
make check
```

That command checks formatting, runs `go vet`, executes the race-enabled test suite, builds every package, and checks the Git diff for whitespace errors.

Build a local executable:

```sh
go build -o ./bin/overtok ./cmd/overtok
./bin/overtok
```

The TUI starts in local mode. Press `r` to collect supported evidence and
refresh committed totals, `1`–`4` to switch views, `?` for help, and `q` to
quit. Set `NO_COLOR` to any non-empty value to disable styled output. The
minimum supported width is 48 columns.

Run the source diagnostic:

```sh
./bin/overtok doctor
```

`doctor` reports source types, health, candidate-file availability,
capabilities, parser compatibility, and privacy-safe local database health. It
intentionally does not print local source or database paths, parse session
contents, or expose persisted observations.

## Repository guide

| Path | Responsibility |
| --- | --- |
| `cmd/overtok` | Native executable entry point |
| `internal/adapters` | Source discovery, ccusage validation, and source-specific parsers |
| `internal/normalize` | Provider-neutral observations, capabilities, and invariants |
| `internal/collect` | Parsing orchestration, deduplication, cumulative reduction, and summaries |
| `internal/application` | Local refresh sequencing and committed dashboard queries |
| `internal/account` | Device login, rotation, revocation, and OS credential-vault storage |
| `internal/doctor` | Privacy-safe source and storage diagnostic reporting |
| `internal/fixturecheck` | Automated fixture privacy validation |
| `internal/modelcatalog` | Versioned model aliases and API-equivalent pricing resolution |
| `internal/store` | Local SQLite lifecycle, observations, aggregates, and diagnostics |
| `internal/tui` | Bubble Tea application shell and local presentation boundary |
| `internal/sync` | Privacy-allowlisted protocol, bounded batching, retry, and dry-run serialization |
| `fixtures` | Synthetic compatibility and regression data |
| `server/postgres` | Hosted PostgreSQL connection and migration boundary |
| `server/account`, `server/auth`, `server/sync`, and `server/httpapi` | Account aggregation, device authorization, idempotent upload, and bounded JSON API |
| `migrations` | Immutable, embedded hosted PostgreSQL schema history |
| `docs` | Cross-cutting architecture and design decisions |

Package-level navigation and ownership are documented in [internal/README.md](./internal/README.md). Cross-cutting documents are indexed in [docs/README.md](./docs/README.md).

## Design principles

- Keep discovery, parsing, normalization, collection, persistence, synchronization, and presentation separate.
- Keep observed model identifiers distinct from canonical model and pricing identifiers.
- Treat unavailable metadata as unavailable, never as zero.
- Make repeated collection deterministic and safe.
- Keep provider-specific rules inside adapters.
- Validate external tool output as a pinned, versioned contract.
- Normalize and commit local state before any future upload attempt.

## Documentation

- [Documentation index](./docs/README.md)
- [Development guide](./docs/development.md)
- [Roadmap](./ROADMAP.md)
- [Architecture](./docs/architecture.md)
- [TUI design direction](./docs/design.md)
- [Contributor and agent guidance](./AGENTS.md)

## Contributing

Read [AGENTS.md](./AGENTS.md), the roadmap, architecture, and design documents before changing the project. Keep fixtures synthetic, use conventional commits, run `make check`, and leave pull requests open for review unless a specific merge is explicitly requested.

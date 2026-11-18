# Overtok

Overtok is a terminal-first application for comparing coding-agent usage in private groups. The current implementation collects supported local Claude and Codex evidence, normalizes token and model metadata, and reports source health without reading ordinary chat data.

The repository is still before persistence, synchronization, authentication, and the interactive TUI. The [roadmap](./ROADMAP.md) describes those later stages without making them part of the current executable.

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
- privacy-safe source diagnostics through `overtok doctor`.

Model names are not allowlisted. A previously unseen, non-empty observed identifier can retain its token counts while canonical resolution and pricing remain unavailable. Reported, resolved, inferred, and unknown attribution are separate states.

## Requirements

- Go 1.24 or newer.
- macOS for the currently implemented discovery paths.
- ccusage 20.0.51 only when exercising the optional ccusage integration. Overtok does not download it at runtime.

The pnpm workspace is reserved for later npm release tooling; it is not needed to build the native command today.

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
| `internal/doctor` | Privacy-safe source and storage diagnostic reporting |
| `internal/fixturecheck` | Automated fixture privacy validation |
| `internal/modelcatalog` | Reserved boundary for model aliases and pricing resolution |
| `internal/store` | Local SQLite lifecycle, observations, aggregates, and diagnostics |
| `internal/sync`, `internal/tui` | Reserved boundaries for later roadmap stages |
| `fixtures` | Synthetic compatibility and regression data |
| `server` and `migrations` | Reserved server and database boundaries |
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

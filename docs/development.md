# Development guide

This guide covers the current native Go workspace. The pnpm workspace is reserved for later npm release tooling and does not participate in the local collection build.

## Prerequisites

- Go 1.24 or newer.
- macOS to exercise the implemented default discovery roots.
- Git for the repository hygiene check.

ccusage is optional. Tests inject a fake executor and read synthetic fixtures, so the verification suite does not require an installed binary or inspect real local agent data.

## Common commands

```sh
make check       # formatting, vet, race tests, build, and diff hygiene
make test        # standard Go tests
make test-race   # race-enabled Go tests
make build       # build every Go package
```

Build and run the command directly:

```sh
go build -o ./bin/overtok ./cmd/overtok
./bin/overtok --version
./bin/overtok             # interactive local TUI
./bin/overtok doctor
```

The Makefile places `GOCACHE` and `GOPATH` under `/tmp` by default. Override those variables when a development environment needs different writable cache locations.

## Test boundaries

- Parser tests consume only committed synthetic fixtures or inline synthetic records.
- Discovery and doctor tests construct temporary home-directory layouts.
- Fixture privacy tests scan every file under `fixtures`, including malformed and non-JSON cases.
- Collection tests cover repeatability, overlap, cumulative counters, resets, partial writes, malformed records, and attribution quality.
- Application and TUI tests inject storage, collection, terminal-size, and keyboard events; they never launch against real provider roots.

Do not point automated tests at a real home directory or provider-owned storage. Real compatibility investigation requires explicit user approval and must produce a newly constructed sanitized regression fixture rather than a copied transcript.

## Adding code

1. Read `AGENTS.md`, the roadmap, architecture, and design guidance.
2. Keep provider rules inside adapters and interfaces at their consuming boundary.
3. Preserve nil versus reported zero and observed versus canonical model identity.
4. Add table-driven tests and synthetic fixtures for source-format behavior.
5. Update the closest package or subsystem documentation when a contract changes.
6. Run `make check` and `git diff --check` before committing.

For stacked work, open and report each parent PR before starting its child branch. Never merge a PR without an explicit request for that PR.

## CI

GitHub Actions runs `make check` on pull requests and pushes to `main` using the Go version declared in `go.mod`. A local clean checkout should pass the same command before a PR is marked ready.

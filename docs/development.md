# Development guide

This guide covers the current native Go workspace. The pnpm workspace is reserved for later npm release tooling and does not participate in the local collection build.

## Prerequisites

- Go 1.25 or newer.
- Node.js 20 or newer plus the repository-pinned pnpm through Corepack, only for npm distribution work.
- macOS to exercise the implemented default discovery roots.
- Git for the repository hygiene check.
- Docker or another PostgreSQL 18 installation only for hosted schema tests.

ccusage is optional. Tests inject a fake executor and read synthetic fixtures, so the verification suite does not require an installed binary or inspect real local agent data.

## Common commands

```sh
make check       # formatting, vet, race tests, build, and diff hygiene
make test        # standard Go tests
make test-race   # race-enabled Go tests
make build       # build every Go package
make release VERSION=1.2.3 # reproducible macOS arm64/x64 archives
pnpm check                 # npm metadata and launcher validation
pnpm test                  # npm preparation and launcher tests
```

Run the hosted schema integration test against an isolated PostgreSQL database:

```sh
OVERTOK_TEST_DATABASE_URL='postgres://overtok:password@localhost:5432/overtok_test?sslmode=disable' make test-postgres
```

The test creates a random schema, applies the embedded migrations twice, checks
ownership and bucket constraints, and removes that schema afterward. Never use
a production database URL. Regular `make check` does not require PostgreSQL;
CI runs the integration target separately against PostgreSQL 18.

Build and run the command directly:

```sh
go build -o ./bin/overtok ./cmd/overtok
./bin/overtok --version
./bin/overtok             # interactive local TUI
./bin/overtok doctor
```

## Local account API

Running account commands locally requires PostgreSQL and a GitHub OAuth app
with Device Flow enabled. The app needs only public identity; do not request
repository or organization scopes.

Configure the API process with:

```sh
export DATABASE_URL='postgres://overtok:password@localhost:5432/overtok?sslmode=disable'
export GITHUB_CLIENT_ID='your-github-oauth-client-id'
export OVERTOK_AUTH_STATE_KEY="$(openssl rand -base64 32)"
export OVERTOK_LISTEN_ADDR='127.0.0.1:8080'
go run ./server/cmd/api
```

Then point the native client at the loopback API:

```sh
OVERTOK_API_URL='http://127.0.0.1:8080' go run ./cmd/overtok login
OVERTOK_API_URL='http://127.0.0.1:8080' go run ./cmd/overtok logout
OVERTOK_API_URL='http://127.0.0.1:8080' go run ./cmd/overtok sync --dry-run
OVERTOK_API_URL='http://127.0.0.1:8080' go run ./cmd/overtok sync
OVERTOK_API_URL='http://127.0.0.1:8080' go run ./cmd/overtok account delete --confirm DELETE
OVERTOK_API_URL='http://127.0.0.1:8080' go run ./cmd/overtok autosync enable
go run ./cmd/overtok autosync status
go run ./cmd/overtok autosync disable
```

Plain HTTP is accepted only for loopback development. Non-loopback API origins
must use HTTPS. The state key encrypts short-lived GitHub device codes and must
be provided through secret management in a deployed environment; it is not a
client credential.

`sync` first runs the normal local collection transaction and absolute-bucket
rebuild. It then uploads committed outbox records in batches of at most 100.
`sync --dry-run` follows the same path through serialization, but prints one
exact request body per line without contacting the API or changing outbox state.
Account deletion is permanent for hosted data and removes the local device
session only after the server confirms deletion. It does not erase local SQLite
usage or provider evidence.

Autosync is a macOS-only, opt-in user launch agent. Enabling it copies the
current native executable to stable private application support, so use a built
release binary rather than `go run` for a lasting schedule. Re-enable it after
an upgrade to refresh that copy. Disabling removes the schedule, copy, and logs
without deleting local usage or credentials.

An authenticated client can inspect the account-level aggregation endpoint:

```sh
curl -H 'Authorization: Bearer OVERTOK-DEVICE-CREDENTIAL' \
  http://127.0.0.1:8080/v1/account/usage
```

The response aggregates active devices and reports every device's revocation
and freshness state. Never paste a real device credential into documentation,
shell history, bug reports, or test fixtures.

The Makefile places `GOCACHE` and `GOPATH` under `/tmp` by default. Override those variables when a development environment needs different writable cache locations.

## Test boundaries

- Parser tests consume only committed synthetic fixtures or inline synthetic records.
- Discovery and doctor tests construct temporary home-directory layouts.
- Fixture privacy tests scan every file under `fixtures`, including malformed and non-JSON cases.
- Collection tests cover repeatability, overlap, cumulative counters, resets, partial writes, malformed records, and attribution quality.
- Application and TUI tests inject storage, collection, terminal-size, and keyboard events; they never launch against real provider roots.
- PostgreSQL integration tests use only synthetic identities and values in a disposable schema.
- Authentication tests use synthetic GitHub responses and never contact a real account.
- Synchronization tests use allowlisted synthetic buckets and verify retry, replay, replacement, conflict, and acknowledgement behavior.
- Account integration tests cover multi-device revocation and complete account-owned data deletion without affecting unrelated accounts.

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

GitHub Actions runs `make check` on macOS and the hosted schema integration test
against PostgreSQL 18 on Linux. Both jobs use the Go version declared in
`go.mod`. A local clean checkout should pass the same applicable commands before
a PR is marked ready.

# Server boundary

The hosted PostgreSQL persistence foundation is implemented in `postgres`. It
opens a pgx connection pool and applies the embedded, checksummed migrations in
one advisory-locked transaction. `auth` and `httpapi` implement GitHub device
authorization, Overtok credential rotation/revocation, and bounded JSON routes.

The schema contains users, stable GitHub identities, independently revocable
devices and hashed credentials, pricing revisions, and privacy-limited absolute
usage buckets. Bucket identity is unique per device. Network code will later
define how equal and newer revisions become no-ops or replacements; the schema
does not implement upload behavior by itself.

The server must never accept prompts, responses, source code, command text,
local paths, project or repository metadata, provider credentials, or raw
source records. Local discovery, provider parsing, cumulative-counter
reduction, and raw-record deduplication remain native-client responsibilities.

The API requires `DATABASE_URL`, `GITHUB_CLIENT_ID`, and a dedicated 32-byte
base64 `OVERTOK_AUTH_STATE_KEY`. `OVERTOK_LISTEN_ADDR` defaults to loopback.
Register the GitHub OAuth app with Device Flow enabled and no repository scopes.
GitHub access tokens are used only in memory for the authenticated-user request
and are never stored or returned to the native client.

Run the disposable-schema integration suite with `make test-postgres` and an
`OVERTOK_TEST_DATABASE_URL`. Do not target production. Do not add distributed
infrastructure, background queues, caches, or deployment configuration before
a measured requirement and the corresponding roadmap stage.

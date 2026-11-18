# Local storage

This package owns the private on-device SQLite database. It does not discover
or parse provider records, and persisted schemas must contain normalized,
privacy-safe fields only.

## SQLite driver

Overtok pins `github.com/ncruces/go-sqlite3` v0.32.0. The project is actively
maintained, supports `database/sql`, tests macOS on both supported
architectures, and does not require CGO because it runs SQLite through wazero.
Version 0.32.0 is the newest release compatible with Overtok's Go 1.24 minimum;
newer driver releases require a newer Go language version. Re-evaluate both
pins together rather than upgrading the driver implicitly.

The application database lives at `Overtok/overtok.db` under the operating
system's user configuration directory. Callers may supply another absolute
path for tests or explicit configuration. Source paths are never stored in the
database location or migration history.

## Lifecycle and migrations

`Open` creates the application directory and database with owner-only
permissions, enables WAL mode, foreign keys, a bounded busy timeout, and
`synchronous=NORMAL`, then applies embedded migrations. These connection-local
settings are part of the open contract; callers must not open the file through
another driver path.

Migration versions are contiguous and immutable. Their names and SHA-256
checksums are recorded in `schema_migrations`. All pending migrations run in
one immediate transaction, so a failed upgrade cannot leave a checkpoint ahead
of its schema. A newer, reordered, renamed, or edited applied migration is an
incompatible database rather than something Overtok repairs silently.

## Observation commits

`CommitObservations` writes normalized observations, the matching source
checkpoint, and adapter health in one transaction. Observation uniqueness is a
SHA-256 fingerprint of source and source-record identity, so rescanning the
same evidence is a no-op. Source-root, session, and last-record identifiers are
also fingerprinted before SQL execution; the schema has no path or raw
source-identifier fields.

`CommitScan` extends the same invariant across every source completed by one
application scan: either all observation/checkpoint/health batches commit, or
none do.

Token categories use nullable decimal text. This preserves unavailable versus
reported zero and supports the complete `uint64` range that SQLite's signed
integer storage cannot represent. Model resolution, observed and canonical
identifiers, pricing keys, measurement quality, counter semantics, versions,
capabilities, and safe warnings remain separate fields.

## Absolute usage buckets

Committed observations reduce into absolute one-minute UTC buckets keyed by
source, surface, observed and canonical model identity, resolution quality,
measurement quality, and normalization version. A higher revision replaces the
entire earlier value. Replaying the same revision is a no-op; an older revision
is ignored; the same revision with different content is rejected.

Only the newest pending revision for each bucket remains in the local outbox.
Its JSON is built from an explicit aggregate payload and has no source record,
session, or path fields. This package does not implement a network client.

Each token category and API-equivalent estimate carries `complete`, `partial`,
or `unavailable` coverage. Period queries add `empty` when no bucket exists.
Aggregation returns an error on unsigned overflow rather than saturating or
wrapping.

Today and the current week begin at midnight in the requested IANA timezone;
weeks begin Monday. The last-30-days window includes the current local calendar
date plus the preceding 29 dates. Boundaries convert to UTC before querying,
and the end includes the current UTC minute. One-minute buckets make modern
IANA offset and daylight-saving boundaries exact.

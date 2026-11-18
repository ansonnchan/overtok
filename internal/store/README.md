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

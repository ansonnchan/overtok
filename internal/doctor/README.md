# Local diagnostics

The `doctor` package turns discovery health, optional parser compatibility, and
the local database state into a stable, human-readable report. It diagnoses
availability only: it does not parse usage records, repair provider storage,
or mutate collected state. Opening an existing database may apply a compatible
embedded schema migration.

## Privacy boundary

Discovery findings contain private candidate paths for later collection, but rendering never reads or prints those fields. Output is built from source kinds, health states, counts, format identifiers, capabilities, and allowlisted warning codes.

External parser stderr is not rendered because it may contain local paths. Parser failures are reduced to `not found`, `unsupported`, `execution_failed`, or `timeout` states.

The configured database path is also private. Reports say whether the default
or a custom location is in use but never print the path. Database checks expose
only schema compatibility, integrity, checkpoint and pending-outbox counts,
last successful collection time, and persisted adapter health.

## Exit status

Exit status `0` means the platform, parser, sources, and initialized database
are healthy. Exit status `1` means the installation can be used but needs
attention, including an uninitialized database, stale collection (24 hours by
default), persisted adapter warnings, source discovery warnings, or parser
incompatibility. Exit status `2` means the database cannot be used safely
because it is locked, corrupt, newer than this binary, or failed another
storage check. Each database failure state includes a focused recovery action.

A source that is not installed is optional and does not fail the command.
Permission failures, unsupported layouts, empty/degraded roots, cancellation,
and incompatible parser versions return status `1`.

Candidate-file counts are not usage-record counts. Discovery has not parsed those files yet, so the renderer deliberately labels them as candidates.

## Testing

CLI and package tests inject synthetic home directories, discovery results, and parser executions. Never point tests at a developer's real home directory or agent storage.

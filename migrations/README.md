# Migrations

Local SQLite migrations are embedded beside `internal/store` so the native
binary can apply them without runtime files. Their lifecycle and immutability
rules are documented in [local storage](../internal/store/README.md).

This directory remains reserved for future server PostgreSQL migrations, which
must stay separate from the local schema history.

Do not add tables for authentication, synchronization, groups, or backend infrastructure before their roadmap stage. Schema comments should explain invariants and ownership instead of duplicating column names.

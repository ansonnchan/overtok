# Migrations

This package embeds the immutable PostgreSQL schema history for the hosted API.
It remains separate from the local SQLite migrations beside `internal/store`.

PostgreSQL files use `NNNN_short_name.sql`, begin at version 1, and remain
contiguous. The server records each name and SHA-256 checksum. Never edit an
applied migration; add the next version instead. A changed checksum, changed
name, gap, or database version newer than the binary is an incompatibility.

All pending migrations run in one transaction under a PostgreSQL advisory
lock. Validate changes with `make test-postgres` and a disposable database URL.
Schema comments should explain privacy, ownership, and unusual invariants
instead of duplicating column names.

Do not add deployment state before its roadmap stage. Server migrations must
never introduce columns for raw local records or prohibited source material.

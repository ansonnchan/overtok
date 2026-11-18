# Migrations

Database migrations begin with local persistence work. This directory intentionally contains no schema yet.

Future migrations must be ordered, immutable after release, and exercised from an empty database in CI. Local SQLite migrations belong to the native storage boundary; PostgreSQL migrations for the later server must remain distinguishable rather than sharing ambiguous files.

Do not add tables for authentication, synchronization, groups, or backend infrastructure before their roadmap stage. Schema comments should explain invariants and ownership instead of duplicating column names.

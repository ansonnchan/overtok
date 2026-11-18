# Source diagnostics

The `doctor` package turns discovery health and optional parser compatibility into a stable, human-readable report. It diagnoses availability only: it does not parse usage records, repair provider storage, or mutate local state.

## Privacy boundary

Discovery findings contain private candidate paths for later collection, but rendering never reads or prints those fields. Output is built from source kinds, health states, counts, format identifiers, capabilities, and allowlisted warning codes.

External parser stderr is not rendered because it may contain local paths. Parser failures are reduced to `not found`, `unsupported`, `execution_failed`, or `timeout` states.

## Exit status

`doctor` returns success when the platform is supported, any installed ccusage binary is compatible, and every present source root is healthy. A source that is not installed is optional and does not fail the command. Permission failures, unsupported layouts, empty/degraded roots, cancellation, and incompatible parser versions return a non-zero status.

Candidate-file counts are not usage-record counts. Discovery has not parsed those files yet, so the renderer deliberately labels them as candidates.

## Testing

CLI and package tests inject synthetic home directories, discovery results, and parser executions. Never point tests at a developer's real home directory or agent storage.

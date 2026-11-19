# Pinned ccusage contract

The native collector does not invoke ccusage. This package retains the reviewed
`20.0.51` compatibility boundary used by releases and diagnostics:

- [`ccusage.lock.json`](../../../ccusage.lock.json) pins the version, schema ID,
  and offline daily JSON commands;
- release tooling rejects a lock that differs from the compiled contract;
- `tokenholic doctor` reports whether an installed binary matches the pin; and
- synthetic Claude and Codex reports validate the expected JSON contract in CI.

The fixture tests reject unknown or duplicate fields, malformed counters,
invalid dates or models, inconsistent totals, and reasoning counts greater than
output. Fixtures must remain synthetic and contain no paths, transcripts, or
credentials.

Keeping this contract does not make ccusage a runtime dependency. tokenholic
never installs, downloads, or executes it during collection. A future adapter
must be wired through the normal discovery and normalization boundaries before
the product can claim that integration.

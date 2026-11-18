# Pinned ccusage contract

This package treats ccusage as an external, versioned parser rather than a library dependency. Overtok accepts only version `20.0.51` and validates its complete JSON contract before creating normalized observations.

The authoritative pin is [`ccusage.lock.json`](../../../ccusage.lock.json). Runtime code and tests must agree with its version, schema identifier, and commands.

## Commands

```text
ccusage daily --json --no-cost --offline --timezone UTC
ccusage codex daily --json --no-cost --offline --timezone UTC
```

`--offline` prevents update or metadata fetches, `--no-cost` keeps pricing outside this collection boundary, and UTC makes daily identities deterministic. Overtok checks `ccusage --version` before running either report.

Overtok does not install or update ccusage. A missing or incompatible binary is reported by `overtok doctor` and collection fails visibly rather than silently falling back to a different schema.

## Validation

Claude and Codex daily reports have different shapes and are decoded separately. Validation rejects:

- an unsupported version;
- unknown or duplicate JSON fields;
- multiple top-level JSON values;
- missing, null, fractional, negative, or overflowing required counters;
- duplicate or invalid dates;
- empty or duplicate model identifiers;
- model breakdowns that disagree with daily or report totals;
- reasoning output greater than total output;
- output beyond the configured process limit.

Pointer fields distinguish a reported zero from a missing or null property. Reasoning output is validated as a subset of output and is not added again when checking `totalTokens`.

## Attribution and identity

Claude `modelBreakdowns` identifiers are retained as `reported`. Codex model-map keys are retained as `inferred` because the external report has already attributed snapshots to models outside Overtok's native record context.

Each daily model row receives a stable identifier derived from the contract schema, source, surface, UTC day, and observed model. Repeating the same report therefore produces the same normalized identities.

The committed files under `fixtures/ccusage` are synthetic contract examples, not a model allowlist. Any non-empty model identifier is accepted when the surrounding pinned schema remains valid.

## Privacy and process safety

Subprocess output is bounded. stderr is never included in returned diagnostics because external tools may print local paths. Tests use an injected executor and synthetic JSON, so the compatibility suite never reads a developer's real agent data.

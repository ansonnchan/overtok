# Source adapters

Adapters are the only packages that understand provider-owned storage layouts and record formats. They discover narrowly scoped evidence, parse compatible records, and emit normalized observations. They do not aggregate leaderboards, persist state, or upload data.

## Discovery boundary

`DefaultMacRoots` lists the currently supported macOS evidence roots:

- Claude Code CLI project-session JSONL;
- Claude Desktop Code sessions;
- Claude Desktop local Agent records under the `rpm` subtree;
- Claude Desktop Cowork `audit.jsonl` records outside that subtree;
- Codex session rollouts, represented as shared local storage when CLI and Desktop cannot be distinguished.

These roots are intentionally narrow. Ordinary Claude Desktop chat and general web/desktop chat storage are out of scope. Discovery is read-only, refuses root symlinks, skips nested symlinks and non-regular files, and records health through stable warning codes.

Candidate paths are local implementation details. They are needed to open a file but must not appear in diagnostics, normalized observations, logs, fixtures, or uploads. Relative paths may be used locally to separate otherwise anonymous files, but are never public metadata.

## Native JSONL parsers

The Claude parser consumes assistant usage records. Claude usage is incremental, and a model directly present in the message is `reported`. Request and message identifiers are hashed before becoming normalized record IDs.

The Codex parser follows `turn_context` records to attribute later token snapshots. That attribution is `inferred`, because the token event does not directly carry the active model. Codex counters are cumulative and are reduced by `internal/collect` only after cross-root deduplication.

Both parsers recover after malformed complete lines and defer an unterminated final line as a partial write. They keep missing token categories nil rather than inventing zeroes.

## ccusage compatibility contract

The `ccusage` package validates the release manifest and supports the optional
version probe shown by `tokenholic doctor`. Native collection does not execute
ccusage. Synthetic fixtures preserve the reviewed external JSON contract for a
future adapter without shipping an unwired collection path. See
[ccusage/README.md](./ccusage/README.md).

## Adding a source format

1. Add a sanitized synthetic fixture before parser logic.
2. Extend fixture privacy validation if the format introduces new structural keys.
3. Keep discovery separate from parsing.
4. Define stable source identity and counter semantics explicitly.
5. Preserve unknown model and token values.
6. Add malformed, partial-write, duplicate, and repeatability tests where applicable.

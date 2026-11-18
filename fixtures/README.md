# Compatibility fixtures

Only synthetic or explicitly approved sanitized source records belong here. Fixtures must not contain prompts, responses, source code, credentials, identifying paths, repository metadata, or raw transcripts.

The committed records use synthetic identifiers and timestamps. `internal/fixturecheck` validates every JSON and JSONL fixture during `go test ./...`, including intentionally malformed compatibility cases.

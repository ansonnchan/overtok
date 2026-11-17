# Overtok contributor guidance

Read these documents before changing the project:

1. [ROADMAP.md](./ROADMAP.md) for scope, milestones, and delivery order.
2. [docs/architecture.md](./docs/architecture.md) for boundaries and data flow.
3. [docs/design.md](./docs/design.md) for TUI behavior and visual direction.

## Product boundaries

Overtok tracks coding-agent usage from supported local evidence. It must not upload prompts, responses, source code, file paths, project names, repository names, branch names, command text, or provider credentials.

Ordinary desktop/web chat is outside the product. Claude Desktop local Code/Agent/Cowork sessions are in scope when they expose compatible local usage records.

Unknown and unavailable values are not zero. Preserve the distinction between reported, resolved, inferred, estimated, and unsupported data.

## Architecture rules

- Keep source discovery, parsing, normalization, persistence, synchronization, and presentation separate.
- Keep observed model IDs separate from canonical model IDs and pricing keys.
- Normalize and commit local state before attempting network upload.
- Make collection and synchronization safe to repeat.
- Prefer absolute versioned buckets over additive upload commands.
- Keep provider-specific behavior inside adapters.
- Do not add distributed infrastructure without a measured need.
- Treat ccusage output as an external versioned contract. Pin it and validate it.

## Implementation style

- Use Go for the native CLI, collector, TUI, and API.
- Use pnpm only for the npm distribution workspace and release tooling.
- Prefer the standard library until a dependency materially reduces complexity.
- Keep packages small and named after one responsibility.
- Define interfaces at the consuming boundary.
- Wrap errors with actionable context.
- Pass `context.Context` through I/O boundaries.
- Avoid mutable package globals.
- Use table-driven tests for parsers, normalization, and protocol behavior.
- Add a sanitized regression fixture for each source-format bug.

## Comments and documentation

- Keep code comments short.
- Explain architectural intent, privacy boundaries, unusual source semantics, and non-obvious invariants.
- Do not restate what a function or line already says.
- Update architecture or design documentation when a change alters their decisions.
- Do not copy prompts, transcript text, paths, or credentials into tests, documentation, commits, or PRs.

## TUI direction

- Keep the interface restrained, readable, and keyboard-first.
- Use terminal defaults plus one warm accent and muted status colors.
- Avoid neon palettes, cyberpunk styling, gradients, excessive borders, and decorative animation.
- Prefer whitespace, alignment, typography, and a small number of panels.
- Preserve meaning without color and on narrow terminals.
- Do not let presentation own collection or synchronization logic.

## Git workflow

- Use a short-lived `codex/` branch for each cohesive workstream.
- Prefer one focused PR over a long-running feature branch.
- GitHub issues are optional; the roadmap workstream IDs are sufficient for planning.
- Use the repository's configured author for all commits.
- Do not add `Co-authored-by`, generated-by, or agent-attribution trailers.
- Keep commit subjects imperative and concise.
- Do not rewrite or discard unrelated user changes.
- Run relevant formatting, tests, and `git diff --check` before committing.

## Review checklist

- Does repeated collection or sync preserve totals?
- Are missing capabilities visible instead of reported as zero?
- Can the payload contain private source material?
- Are model aliases and inferred mappings labelled honestly?
- Does the change work without color and at the documented minimum width?
- Is the behavior covered by unit tests or sanitized fixtures?
- Did the change add complexity that belongs in a later milestone?

# TUI design direction

Status: local native application shell and views implemented
Last updated: November 18, 2026

## Intent

The Overtok TUI should feel calm, compact, and deliberate. It is a metrics application, not a simulated hacker console.

The supplied Tokenboard reference is useful for its hierarchy, warm accent, restrained dark surfaces, clear table rows, selected-user emphasis, and compact summary cards. Overtok should adapt those principles to terminal constraints without reproducing the web layout or branding.

The presentation layer begins after local persistence and model resolution.
`overtok doctor` remains plain diagnostic output. The TUI consumes
presentation-ready application snapshots and must not absorb discovery,
collection, persistence, or future synchronization responsibilities.

## Visual principles

- Prefer the terminal's background rather than painting the entire viewport.
- Use one warm accent for selection and primary actions.
- Use muted foreground colors for metadata and separators.
- Reserve green, amber, and red for semantic status.
- Use whitespace and alignment before adding borders.
- Keep border styles consistent and sparse.
- Avoid neon, cyberpunk motifs, gradients, glow effects, ASCII art backgrounds, and decorative animation.
- Keep text readable in monochrome and high-contrast environments.

### Initial adaptive palette

The exact Lip Gloss colors should be tested across light and dark terminals.

| Role | Direction |
| --- | --- |
| Primary text | Terminal foreground or soft off-white |
| Muted text | Neutral gray |
| Accent | Warm coral/terracotta |
| Success | Muted teal/green |
| Warning | Muted amber |
| Error | Restrained red |
| Selected row | Accent edge plus subtle adaptive background |

Color is never the only carrier of meaning. Pair status colors with text, symbols, or labels.

## Information architecture

The local native MVP has four TUI areas:

1. **Overview** — personal totals, recent periods, and collection freshness.
2. **Sources** — local adapter coverage, source health, and availability.
3. **Models** — model usage, token categories, and attribution quality.
4. **Settings** — keyboard help, local settings, privacy, and diagnostics.

Authentication, groups, multiplayer leaderboards, and synchronization controls
remain deferred until the local application is complete.

## Responsive layout

### Wide terminals

At 88 columns or more:

```text
Overtok · local usage
[1 Overview]    2 Sources    3 Models    4 Settings

Overview
[ready] Local data loaded
Today ...

1–4 views · tab navigate · r refresh · ? help · q quit
```

Whitespace and short rows provide hierarchy without surrounding the screen in
borders. Data-heavy views may add a second column only when it leaves readable
room for both columns.

### Narrow terminals

From 48 through 87 columns:

- keep navigation on one compact row;
- stack summary information;
- remove decorative columns before truncating labels or totals;
- abbreviate large values consistently; and
- preserve every action through keyboard shortcuts.

The minimum supported width is 48 columns. Below it, the application presents
a resize instruction while preserving help and quit controls.

## Future leaderboard behavior

The following guidance applies only after authentication and multiplayer work
is explicitly started; it is not part of the local native MVP.

Each row should prioritize:

```text
rank  display name  primary metric  model/source mix
```

Optional trend data appears only when the comparison window is meaningful and space permits.

The current user row uses an accent edge and `you` label. Do not rely on a full-row bright background.

The period selector contains:

```text
Today | This week | 30 days | All time
```

The metric selector contains:

```text
Tokens | Output | API estimate
```

Use `API estimate`, not `spent`, to avoid presenting subscription usage as actual billing.

## Overview and health

The overview should answer these questions without navigation:

- How much have I used in the selected period?
- Which coding agents and models contributed?
- When did this device last collect?
- Is any source partial, unsupported, or unhealthy?

Warnings are concise and actionable:

```text
Codex model unavailable for 2 legacy sessions
Claude Desktop source changed; run `overtok doctor`
3 buckets pending locally
```

Do not turn missing coverage into a zero-value chart.

## Interaction

Initial keyboard conventions:

| Key | Action |
| --- | --- |
| `1`–`4` | Switch main area |
| `tab` / `shift+tab` | Move between controls |
| `j` / `k`, arrows | Move selection |
| `r` | Refresh current view |
| `?` | Help |
| `q` | Quit |

Avoid modal dialogs when a focused inline confirmation is sufficient. Destructive actions require explicit confirmation.

## Motion and feedback

- Use a small spinner only for active collection or refresh.
- Avoid continuous animations while idle.
- Preserve the last stable data while refreshing.
- Show success briefly, then return to freshness/status text.
- Never block navigation on a background refresh.

## Content style

- Use short labels and plain language.
- Prefer `Last synced 4m ago` over protocol terminology.
- Say `Could not read Codex sessions` before showing a technical cause.
- Label inferred and unavailable values explicitly.
- Keep help text one line where possible.

## Accessibility

- Support `NO_COLOR`.
- Test light and dark terminal backgrounds.
- Maintain usable contrast for selected and muted states.
- Do not use color alone for rank movement or errors.
- Avoid Unicode-only meaning; provide ASCII-safe fallbacks.
- Respect terminal width and avoid horizontal scrolling in primary views.

## Non-goals

- Pixel-perfect reproduction of the supplied web reference.
- Avatars or image protocols in the MVP.
- Dense charts when a number, bar, or table communicates the same result.
- Theme customization before the default experience is stable.
- Mouse-first interaction.

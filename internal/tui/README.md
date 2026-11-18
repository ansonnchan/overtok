# Terminal application

This package is Overtok's presentation boundary. It converts privacy-safe
dashboard snapshots from the application interface into Overview, Sources,
Models, and Settings pages, and requests refresh through that same interface.
It must not import provider adapters or query SQLite directly.

The Overview uses Today as its current period and shows Today, This week,
30 days, and All time together. Sources renders persisted adapter health;
Models retains reported, resolved, inferred, and unknown attribution; Settings
explains the local-only privacy boundary. API-equivalent values always include
the word `estimate`, and missing coverage renders as `no data`, `partial`, or
`unavailable` rather than zero.

## Dependency pin

Overtok pins Bubble Tea v1.3.10 and Lip Gloss v1.1.0. Bubble Tea v1.3.10 is the
newest v1 release and supports the project's Go 1.24.0 minimum. Bubble Tea v2
requires at least Go 1.24.2 at its initial release and Go 1.26 in current patch
releases, so adopting it would silently raise the supported toolchain.

## Layout and accessibility

The minimum supported width is 48 columns. From 48 through 87 columns the
shell uses compact navigation and help text; at 88 columns it uses the roomy
header. Below 48 columns it shows a resize instruction while preserving help
and quit controls.

`NO_COLOR` is represented by `Config.NoColor`. In that mode the shell emits no
styling escape sequences. Loading, empty, warning, ready, and error states use
text labels so their meaning is preserved without color.

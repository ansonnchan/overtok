# Internal packages

The packages under `internal` form the native application's private implementation. Their boundaries follow the local data flow, so source-specific behavior cannot leak into persistence, synchronization, or presentation.

```text
local evidence
    -> adapters
    -> normalize
    -> collect
    -> future store
    -> future sync and TUI

doctor reads discovery health and parser compatibility without collecting records
fixturecheck protects committed test data independently of runtime collection
```

## Package ownership

| Package | Owns | Does not own |
| --- | --- | --- |
| `adapters` | Source discovery, source formats, and external parser contracts | Cross-source totals, persistence, or UI |
| `normalize` | Provider-neutral records, capability vocabulary, and validation | Filesystem discovery or provider parsing |
| `collect` | Adapter coordination, deduplication, cumulative reduction, and summaries | Database transactions or uploads |
| `doctor` | Redacted source and parser diagnostics | Usage collection or repair |
| `fixturecheck` | Privacy validation for committed fixtures | Runtime validation of user data |
| `modelcatalog` | Future canonical model and pricing resolution | Source parsing |
| `store` | Future local persistence | Source-specific behavior |
| `sync` | Future privacy-limited synchronization | Raw local records |
| `tui` | Future presentation of committed local state | Collection or synchronization logic |

Interfaces should be declared at the package that consumes them. Provider-specific identifiers may pass through normalized contracts as observed values, but provider-specific filesystem and schema rules stay in adapters.

## Documentation convention

Every package has a package comment describing its boundary. Complex subsystems may also have their own README when developers need a map before reading individual files. Implementation comments document privacy constraints, source semantics, and invariants that are not clear from types and control flow; they should not narrate ordinary Go syntax.

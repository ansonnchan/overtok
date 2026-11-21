# Normalized usage contract

The `normalize` package is the provider-neutral boundary between source adapters and every later stage. Adapters may understand Claude or Codex records, but collection, persistence, synchronization, and presentation should depend on normalized observations instead of provider JSON.

## Absence is not zero

Every token category is a pointer. A non-nil pointer to `0` means the source reported that category with a zero value. A nil pointer means the category was unavailable. Consumers must preserve that difference rather than filling unavailable categories with zeroes.

Reasoning output is subset metadata when a source exposes it. It is already included in output and is therefore excluded from `KnownTotal`.

Cache writes follow the same subset rule. `InputCacheWrite` is the total written at every cache lifetime and is the only cache-write value in `KnownTotal`. `InputCacheWrite5m` and `InputCacheWrite1h` split that total when the source reports a time-to-live. A nil lifetime means the time-to-live is unknown, not that nothing was written at it; `CacheWriteUnknownTTL` returns the part of the total no lifetime accounts for. Validation rejects lifetimes without a total or lifetimes that sum above it.

## Model identity

`ObservedModelID` is the identifier present in or attributable from source evidence. It is intentionally separate from the future canonical model identifier, provider, and pricing key.

Resolution values mean:

- `reported`: the source directly supplied the observed model identifier;
- `resolved`: tokenholic mapped an observed identifier to canonical metadata;
- `inferred`: the identifier is attributable only through surrounding source context and carries a warning;
- `unknown`: the available record cannot be attributed safely.

New observed model strings do not require an allowlist update. Unknown pricing must not discard their token counts.

## Count and measurement semantics

Incremental observations represent usage contributed by one record. Cumulative snapshots represent counters up to a point in a session and must be reduced before aggregation. Measurement quality records whether a value was reported by the source, derived deterministically, or estimated.

These axes are independent. For example, a reported cumulative counter becomes a derived incremental observation after reduction.

## Capabilities and health

Capabilities describe how a source can provide each field, including explicit `unsupported` states. Source health describes whether a compatible evidence root was detected and readable. Neither contract converts missing coverage into a zero usage value.

## Validation

`Observation.Validate` protects the boundary by checking source/surface combinations, version identifiers, model-resolution requirements, token availability, and warning safety. Validate observations before persistence or serialization so downstream packages do not need to reinterpret malformed provider data.

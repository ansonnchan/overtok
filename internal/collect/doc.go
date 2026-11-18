// Package collect coordinates source adapters without owning discovery or
// persistence. It deduplicates raw observations before reducing cumulative
// counters, which keeps rescans and overlapping source roots idempotent.
package collect

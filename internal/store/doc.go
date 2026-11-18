// Package store reserves local persistence. It will atomically commit normalized
// observations, source checkpoints, aggregate revisions, and the sync outbox;
// source-specific parsing does not belong here.
package store

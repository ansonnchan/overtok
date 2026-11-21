// Package codex parses supported Codex rollout events into normalized usage
// observations and attributes them to the active turn's model.
//
// Current rollouts report each request's usage beside the session's running
// total, so every request becomes one incremental observation identified by
// its session, turn, and running total. A forked or resumed rollout replays
// its parent's history under new timestamps; because that identity ignores
// the timestamp and the file, each replayed request is recognized as a copy
// of its original. Rollouts that carry only running totals are emitted as
// cumulative snapshots for the collector to reduce.
package codex

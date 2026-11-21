// Package claude parses supported Claude coding-session usage records into
// normalized incremental observations without retaining transcript content.
//
// Locally generated assistant lines (the "<synthetic>" model and API error
// notices) are skipped because they carry no model usage. Cache writes keep
// their 5-minute and 1-hour split when the record reports one.
package claude

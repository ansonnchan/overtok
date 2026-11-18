// Package cli parses the native command line and returns process exit codes.
// It keeps process termination in cmd/overtok and injects diagnostics in tests so
// test runs never inspect a developer's real agent storage.
package cli

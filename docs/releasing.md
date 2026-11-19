# Release artifacts

Tokenholic releases checksummed native archives for macOS arm64 and x64. Release
builds disable CGO, remove local filesystem paths and VCS metadata, inject the
semantic version into `tokenholic --version`, and verify each Mach-O architecture
before packaging it.

Each archive contains the native `tokenholic` executable and
`ccusage.lock.json`. The lock file is the pinned external-parser contract; the
application never downloads or replaces the optional ccusage executable at
runtime. Keeping the contract beside the binary makes the parser requirement
auditable without expanding the release's trust boundary.

Build artifacts from a clean checkout with:

```sh
make release VERSION=1.2.3
```

The command writes both archives and `SHA256SUMS` under `dist/`. Archive entry
ordering, ownership, modes, timestamps, and gzip headers are normalized. Given
the same source, Go toolchain, version, and pinned inputs, it produces the same
bytes.

Pushing an existing semantic-version tag such as `v1.2.3` runs the release
workflow. The workflow builds the artifacts on macOS, uploads them as workflow
artifacts, and publishes them to the matching GitHub release. It then verifies
those checksums again, prepares exact-version platform packages, tests the npm
workspace, and publishes `@tokenholic/darwin-arm64`, `@tokenholic/darwin-x64`, and
finally `@tokenholic/cli`. Configure the GitHub Actions `NPM_TOKEN` secret with
publish access to the `@tokenholic` scope before creating a release tag.
The publisher is restart-safe and skips exact package versions that an earlier
attempt completed. Verify a published version from a clean macOS account with
`npx --yes @tokenholic/cli@VERSION --version`.

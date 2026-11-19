# npm distribution workspace

`@tokenholic/cli` is a small CommonJS launcher that selects one optional native
package from the current operating system and CPU. It forwards arguments
directly without invoking a shell. Unsupported platforms and incomplete native
packages fail with an actionable message instead of falling back to a different
binary.

When a user explicitly enables autosync, the native application copies itself
out of the package-runner cache before installing the launch agent. npm package
installation never enables background work automatically.

The native package directories intentionally do not contain committed
binaries. During a tagged release, `scripts/prepare-npm-release.mjs` verifies
the GitHub artifact checksums, extracts the matching release archives, copies
only the native binary and pinned parser contract, and assigns one exact version
to all three packages. The arm64 and x64 packages publish before the umbrella
package so an install can never resolve a new launcher before its binaries.
Publication is restart-safe: the publisher checks each exact registry version,
skips packages already completed by an earlier attempt, and preserves the same
native-first ordering on retry.

Run metadata, launcher, and preparation tests with:

```sh
pnpm check
pnpm test
```

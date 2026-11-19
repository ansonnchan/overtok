# macOS autosync

Autosync is opt-in. `overtok autosync enable` copies the currently running
native binary to a stable private application-support path and installs a
per-user `launchd` agent. The persistent agent runs `overtok sync` at login and
hourly while the user session is active, so npm cache cleanup and restarts do
not invalidate its executable path.

The launch agent stores only the API origin, an optional explicit `CODEX_HOME`,
and `NO_COLOR=1`. Device credentials remain in the OS credential vault. Stdout
and stderr go to private Overtok log files; the sync command emits stable,
path-redacted summaries and errors rather than payload bodies or provider data.

Scheduled and manual sync share a non-blocking advisory lock. A second process
exits visibly instead of scanning or uploading concurrently. The lock file
remains in place across runs so every process locks the same inode.

`overtok autosync disable` unloads the agent and removes its plist, stable binary,
and logs. It does not delete local SQLite usage, source evidence, the lock file,
or the OS-vault session. Re-run `autosync enable` after upgrading to refresh the
stable binary deliberately; upgrades never enable scheduling without consent.

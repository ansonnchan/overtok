# Native multiplayer boundary

This package owns the native client's private-group HTTP contract. It loads an
Tokenholic device session through an injected credential-vault interface, calls
only the group and leaderboard routes, and returns presentation-safe snapshots
to the CLI and TUI.

It does not collect local evidence, read SQLite, aggregate ranks, implement
server authorization, or persist group responses. Invite plaintext is accepted
only for a join request or returned once by invite creation; the TUI masks join
input and does not retain it after submission.

Remote origins require HTTPS, with loopback HTTP allowed for development. The
client disables redirects so device credentials cannot be forwarded to a
different origin. Unauthorized responses become a signed-out state without
rendering response bodies or credential details.

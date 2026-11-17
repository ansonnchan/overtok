# overtok

A terminal-first, multiplayer leaderboard for coding-agent usage.

Overtok is currently implementing its local collection feasibility milestone. See the [implementation roadmap](./ROADMAP.md) for the MVP scope, architecture, PR-sized workstreams, and extension plan.

## Development

The native command requires Go 1.24 or newer. A clean checkout can run the complete local verification suite with:

```sh
make check
```

Build and run the minimal executable with:

```sh
go build -o ./bin/overtok ./cmd/overtok
./bin/overtok
```

Project documentation:

- [Roadmap](./ROADMAP.md)
- [Architecture](./docs/architecture.md)
- [TUI design direction](./docs/design.md)
- [Contributor and agent guidance](./AGENTS.md)

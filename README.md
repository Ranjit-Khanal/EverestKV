# EverestKV

EverestKV is a Redis-inspired in-memory key-value store written in Go. It is a systems / core-engineering project: networking, protocol design, concurrency, and storage internals—not an application framework or API wrapper.

The goal is to reimplement the *ideas* behind Redis in a small, readable codebase. Compatibility is a learning tool, not a product requirement.

## Engineering focus

| Area | What we care about |
|------|--------------------|
| Architecture | Clear layering, small packages, explicit boundaries |
| Networking | TCP accept loop, per-connection lifecycle |
| Protocol | RESP2 framing, parse/encode without hand-waving |
| Concurrency | Goroutine model, shared-state safety (as the store lands) |
| Storage | In-memory engine, later TTL / eviction / persistence |
| Operability | Simple build (`make`), thin CLIs, debuggable wire format |

We prefer depth over feature count. A correct `GET`/`SET` path with a clean architecture beats a large surface of half-finished commands.

## Architecture

```
Client (everestkv-cli / redis-cli)
        │  TCP :6379  (RESP2)
        ▼
┌───────────────────────────────┐
│  cmd/everestkv                │  process entry
└───────────────┬───────────────┘
                ▼
┌───────────────────────────────┐
│  internal/server              │  accept, session loop
└───────────────┬───────────────┘
                ▼
┌───────────────────────────────┐
│  internal/command             │  registry + handlers (GET, SET, …)
└───────┬───────────────┬───────┘
        │               │
        ▼               ▼
┌───────────────┐ ┌─────────────────┐
│  pkg/resp     │ │ internal/store  │
│  parse/write  │ │ in-memory map   │
└───────────────┘ └─────────────────┘

  planned on store: TTL / eviction / persistence
```

**Layers**

1. **Transport** — TCP listen/accept; one goroutine per connection.
2. **Protocol** — `pkg/resp` turns the byte stream into values and back.
3. **Commands** — `internal/command` maps names to handlers; add new commands here.
4. **Storage** — `internal/store` holds keys/values behind a `RWMutex`.
5. **Server** — `internal/server` only owns connections and calls `command.Dispatch`.

Layout follows [golang-standards/project-layout](https://github.com/golang-standards/project-layout): `cmd/` for binaries, `internal/` for private app code, `pkg/` for reusable libraries.

## Project layout

```
cmd/everestkv/          Server entrypoint
cmd/everestkv/cli/      Interactive RESP client
internal/server/        TCP accept + session loop
internal/command/       Command registry and handlers
internal/store/         In-memory key-value engine
pkg/resp/               RESP2 encode/decode
Makefile                build / run targets
```

## Status

- RESP2 parser and writer
- TCP server + interactive CLI
- In-memory store with `GET` / `SET`

Next: `DEL`, TTL, persistence.

## Build & run

```bash
make build          # bin/everestkv + bin/everestkv-cli
make run            # start server on :6379
make run-cli        # interactive client (server must be up)
make clean
```

Supported commands today: `PING`, `ECHO <msg>`, `GET <key>`, `SET <key> <value>`, `QUIT`.

You can also use `redis-cli` against `:6379` for the same commands.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). We prioritize architecture and core engineering quality over drive-by features.

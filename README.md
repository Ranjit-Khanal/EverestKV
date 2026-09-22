# EverestKV

EverestKV is a Redis-inspired in-memory key-value store written in Go. It is built as **local-first infrastructure for Nepali builders**: a small, understandable KV you can run on your own VPS or laptop so basic caching and sessions do not require a hosted Redis dependency.

It is a systems / core-engineering project — TCP networking, RESP protocol, concurrency, and clean package boundaries — not an HTTP CRUD wrapper or API framework.

> **Not a full Redis replacement (yet).** We aim at the subset most small apps need: strings, TTL, persistence, and a stable RESP-compatible wire protocol. Depth over feature count.

## Why EverestKV?

Many teams use Redis only for cache, sessions, or OTPs. EverestKV targets that niche — especially for builders in Nepal who want infrastructure they can read, host, and own:

- One binary, simple deploy (`make build` / `make run`)
- RESP-compatible (works with `redis-cli` and familiar mental models)
- Goroutine-based concurrency (Go’s model — not a hand-rolled `epoll` loop)
- Open source with an architecture-first codebase for contributors

Compatibility with Redis ideas is a product tool, not a promise to clone every command.

## Engineering focus

| Area | What we care about |
|------|--------------------|
| Architecture | Clear layering, small packages, explicit boundaries |
| Networking | TCP accept loop, one goroutine per connection |
| Protocol | RESP2 framing, parse/encode without hand-waving |
| Concurrency | Go runtime + safe shared state (`RWMutex` on the store) |
| Storage | In-memory engine; next: TTL / eviction / persistence |
| Operability | Thin CLIs, debuggable wire format, easy local hosting |

We prefer depth over feature count. A correct `GET`/`SET` path with clean boundaries beats a large surface of half-finished commands.

## Architecture

```
Client (everestkv-cli / dashboard / redis-cli)
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

  planned on store: TTL / eviction / file persistence
```

`everestkv-cli` and `cmd/everestkv/web` (the dashboard) are both thin frontends over
`internal/client`, which is the one place that turns operations into RESP2 requests. Neither
speaks the wire protocol itself, so both stay in lockstep with whatever `internal/command`
supports.

**Layers**

1. **Transport** — TCP listen/accept; one goroutine per connection.
2. **Protocol** — `pkg/resp` turns the byte stream into values and back.
3. **Commands** — `internal/command` maps names to handlers; add new commands here.
4. **Storage** — `internal/store` holds keys/values behind a `RWMutex`.
5. **Server** — `internal/server` only owns connections and calls `command.Dispatch`.
6. **Client** — `internal/client` wraps a RESP2 connection with `Do`/`Get`/`Set`/`Execute`; the CLI and the web dashboard both build on it.

Layout follows [golang-standards/project-layout](https://github.com/golang-standards/project-layout): `cmd/` for binaries, `internal/` for private app code, `pkg/` for reusable libraries.

## Project layout

```
cmd/everestkv/          Server entrypoint
cmd/everestkv/cli/      Interactive RESP client
cmd/everestkv/web/      Web dashboard (HTTP API + embedded UI)
internal/server/        TCP accept + session loop
internal/command/       Command registry and handlers
internal/store/         In-memory key-value engine
internal/client/        Shared RESP2 client used by the CLI and dashboard
pkg/resp/               RESP2 encode/decode
Makefile                build / run targets
```

## Status

- RESP2 parser and writer
- TCP server + interactive CLI + web dashboard
- In-memory store with `GET` / `SET`
- Command registry (handlers outside `server.go`)

**Roadmap (minimum useful KV):** `DEL`, TTL / `EXPIRE`, snapshot persistence to disk, then broader Redis-compatible commands.

## Build & run

```bash
make build          # bin/everestkv + bin/everestkv-cli + bin/everestkv-web
make run            # start server on :6379
make run-cli        # interactive client (server must be up)
make run-web        # dashboard on :8080, backed by the server on :6379
make clean
```

Supported commands today: `PING`, `ECHO <msg>`, `GET <key>`, `SET <key> <value>`, `KEYS *`, `QUIT`.
`KEYS` only supports the `*` pattern (every key) for now — no globbing yet.

You can also use `redis-cli` against `:6379` for the same commands.

## Web dashboard

`bin/everestkv-web` is a small HTTP server with no storage logic of its own: every request it
serves turns into the same RESP2 command a CLI user would type, over `internal/client`.

```bash
make run          # server on :6379
make run-web      # dashboard on :8080 (flags: -addr, -server)
```

Then open `http://localhost:8080`. The UI has a GET/SET form for quick lookups, a "Stored Keys"
table listing every key/value pair (backed by `KEYS *`, refreshed automatically), and a console
that runs any raw command line, mirroring the CLI prompt. `EXIT`/`QUIT` are rejected in the
console since the dashboard holds one shared connection across all browser tabs.

REST endpoints, if you want to script against the dashboard directly instead of the RESP port:

| Method | Path | Body / Query | Notes |
|--------|------|---------------|-------|
| GET | `/api/status` | — | Connection health (pings the backing server) |
| GET | `/api/get` | `?key=` | `{"found": false}` for a missing key, not an error |
| POST | `/api/set` | `{"key","value"}` | |
| GET | `/api/keys` | — | `{"entries":[{"key","value"}, ...]}` for every key |
| POST | `/api/command` | `{"line":"SET a b"}` | Runs any command line; `EXIT`/`QUIT` blocked |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). We prioritize architecture and core engineering — help us make local-first infrastructure Nepal can actually own.

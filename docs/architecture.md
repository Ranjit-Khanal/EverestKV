# Architecture

This document explains how EverestKV is put together: which package owns what, how a request
travels from an HTTP request to the store and back, and where the concurrency boundaries are. For the
on-disk engine specifically, see [storage-engine.md](storage-engine.md).

## Design principles

- **Layered, one-way dependencies.** HTTP → storage. Lower layers never import higher ones:
  `internal/store` knows nothing about HTTP.
- **Standard library only.** `go.mod` has no dependencies. New modules need a strong justification.
- **Go's concurrency model, not a hand-rolled event loop.** `net/http` runs one goroutine per
  connection. The Go runtime's netpoller does the epoll/kqueue work.
- **Depth over breadth.** A small API with correct semantics, instead of many half-finished
  endpoints.

## Package map

```
cmd/everestkv/            main: server binary (everestkv)
cmd/everestkv/cli/        main: interactive client (everestkv-cli)
cmd/everestkv/web/        main: HTTP dashboard (everestkv-web), UI embedded via go:embed

internal/server/          HTTP API: routes, handlers, graceful shutdown
internal/client/          HTTP client shared by the CLI and dashboard
internal/store/           Storage: in-memory Store (used today) + LSM DB engine (in progress)
  wal/                    Segmented, checksummed write-ahead log
  memtable/               Skip-list memtable
  sstable/                SSTable writer (on-disk sorted table format)
  manifest/               Atomic record of live SSTables / obsolete WAL segments
```

`internal/` packages cannot be imported from outside this module.

### Ownership rules

| Package             | Owns                                    | Must not own                         |
|---------------------|-----------------------------------------|--------------------------------------|
| `cmd/*`             | Flag parsing, wiring, `main`            | Business logic                       |
| `internal/server`   | Routes, request validation, status codes | Storage internals                   |
| `internal/client`   | Turning operations into HTTP requests, CLI command-line parsing | Storage |
| `internal/store`    | Keys, values, durability                | HTTP                                 |

## Shutdown

On SIGINT or SIGTERM, `cmd/everestkv` calls `Server.Shutdown` with a 10-second timeout:

`Server.Shutdown` wraps `http.Server.Shutdown`:

1. The listener is closed, so no new connections are accepted.
2. Idle keep-alive connections are closed immediately. A request in progress finishes and writes
   its response, and then its connection is closed.
3. `Shutdown` returns `nil` once every connection is closed. If the timeout expires first (for
   example, a client that stopped sending a request body or stopped reading a large response),
   `Shutdown` force-closes the remaining connections with `http.Server.Close` and returns
   `context.DeadlineExceeded`.

`Serve` then returns `server.ErrServerClosed`. When `Shutdown` returns `nil`, no request is
running, so the store can be closed safely. After a forced close, handlers may still be
finishing. A second signal during the wait kills the process immediately.

## Request lifecycle

What happens when a client sends `PUT /v1/kv/greeting` with body `namaste`:

```
client ──HTTP──▶ http.Server                   one goroutine per connection (net/http)
                    │
                    ▼
                routeKV                        path starts with /v1/kv/? → key = "greeting"
                    │                          (otherwise → ServeMux: /v1/ping, /v1/keys)
                    ▼
                handleKV                       switch on method
                    │  PUT: read body, capped at MaxValueBytes (32 MiB)
                    ▼
                store.Set("greeting", "namaste")
                    │
                    ▼
                204 No Content
```

`routeKV` reads the key from the escaped path itself instead of using a `ServeMux` pattern.
`ServeMux` cleans the decoded path and redirects, which would silently change keys such as `a//b`
or `x/../y`.

Error handling at each step:

| Failure                                    | Result                                        |
|--------------------------------------------|-----------------------------------------------|
| Empty key, or invalid percent-escape       | `400 {"error": ...}`                          |
| Key not found (`GET`, `HEAD`, `DELETE`)    | `404 {"error": "key not found"}`              |
| Unsupported method                         | `405` with an `Allow` header                  |
| `PUT` body over 32 MiB                     | `413`                                         |
| Unknown path                               | `404` from `ServeMux`                         |
| Client sends headers too slowly            | Connection closed after `ReadHeaderTimeout` (10s) |

Each HTTP connection handles one request at a time. Clients get parallelism by opening more
connections, which Go's `http.Client` does automatically.

## Concurrency model

- **Server:** `net/http` runs one goroutine per connection. Nothing is shared between requests
  except the store.
- **`store.Store`:** a `map[string]string` behind a `sync.RWMutex`. `GET` and `KEYS` take the read
  lock and `PUT`/`DELETE` take the write lock, so every request is atomic with respect to the
  others.
- **`client.Client`:** wraps an `http.Client`, so it is safe to share across goroutines, and it
  pools connections. The web dashboard uses one `Client` for all browser requests.
- **`store.DB` (LSM engine):** writers share a read lock so their WAL appends can be batched by
  group commit. Memtable rotation takes the write lock, and a single background goroutine flushes
  memtables. Details are in [storage-engine.md](storage-engine.md#concurrency).

## Frontends

The CLI (`everestkv-cli`) and the dashboard (`everestkv-web`) contain **no protocol code**. Both
use `internal/client`, which is the only place that builds HTTP requests and interprets responses.
`Client.Execute` turns a command line such as `SET city Kathmandu` into the matching request and
formats the result, so the CLI and the dashboard console print identical output. A new command
added to `Client.Execute` therefore works in both frontends immediately, with no frontend changes.

## Current state vs. target

The storage layer is in the middle of a transition:

- **Today:** `internal/server` constructs `store.New()`, the in-memory map. Data is lost on restart.
- **In progress:** `store.DB` is a working LSM engine (WAL, group commit, memtable, SSTable
  flush and point reads, manifest, recovery) with its own tests. Data written to it survives a
  restart or crash. It has not been connected to the server yet.

Connecting the server to `store.DB` requires, at minimum: a `/v1/keys` equivalent (an iterator across memtables and SSTables), a data-directory flag, and
calling `DB.Close` after `Server.Shutdown` returns (see [Shutdown](#shutdown)). The roadmap in the [README](../README.md#roadmap) tracks this work.

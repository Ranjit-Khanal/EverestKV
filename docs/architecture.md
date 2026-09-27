# Architecture

This document explains how EverestKV is put together: which package owns what, how a request
travels from a socket to the store and back, and where the concurrency boundaries are. For the
on-disk engine specifically, see [storage-engine.md](storage-engine.md).

## Design principles

- **Layered, one-way dependencies.** Transport → protocol → dispatch → storage. Lower layers never
  import higher ones: `pkg/resp` knows nothing about commands, and `internal/store` knows nothing
  about TCP or RESP.
- **Standard library only.** `go.mod` has no dependencies. New modules need a strong justification.
- **Go's concurrency model, not a hand-rolled event loop.** One goroutine per connection. The Go
  runtime's netpoller does the epoll/kqueue work.
- **Depth over breadth.** A small command set with correct semantics, instead of many
  half-finished commands.

## Package map

```
cmd/everestkv/            main: server binary (everestkv)
cmd/everestkv/cli/        main: interactive client (everestkv-cli)
cmd/everestkv/web/        main: HTTP dashboard (everestkv-web), UI embedded via go:embed

internal/server/          TCP listener, per-connection session loop
internal/command/         Command registry and handlers
internal/client/          RESP2 client shared by the CLI and dashboard
internal/store/           Storage: in-memory Store (used today) + LSM DB engine (in progress)
  wal/                    Segmented, checksummed write-ahead log
  memtable/               Skip-list memtable
  sstable/                SSTable writer (on-disk sorted table format)
  manifest/               Atomic record of live SSTables / obsolete WAL segments

pkg/resp/                 RESP2 parser and writer (importable by other projects)
```

`internal/` packages cannot be imported from outside this module. `pkg/resp` is the only package
meant for outside use.

### Ownership rules

| Package             | Owns                                    | Must not own                         |
|---------------------|-----------------------------------------|--------------------------------------|
| `cmd/*`             | Flag parsing, wiring, `main`            | Business logic                       |
| `internal/server`   | Connections, session loop               | Command implementations              |
| `internal/command`  | Handlers, argument validation, replies  | Sockets, accept loop                 |
| `internal/client`   | Turning operations into RESP requests   | Storage, HTTP                        |
| `pkg/resp`          | RESP2 encode/decode                     | Commands, storage                    |
| `internal/store`    | Keys, values, durability                | TCP, RESP                            |

## Shutdown

On SIGINT or SIGTERM, `cmd/everestkv` calls `Server.Shutdown` with a 10-second timeout:

1. The listener is closed, so no new connections are accepted.
2. Each connection's read deadline is set to now. A client waiting idle for its next command is
   disconnected immediately. A connection in the middle of a command finishes it and writes the
   reply, runs any complete commands it had already buffered, and then exits on its next read.
3. `Shutdown` returns once every connection goroutine has exited. If the timeout expires first
   (for example, a client that stopped reading a large reply), the remaining connections are
   force-closed and `Shutdown` returns `context.DeadlineExceeded`.

`Serve` then returns `server.ErrServerClosed`. When `Shutdown` returns, no command is running, so
the store can be closed safely. A second signal during the wait kills the process immediately.

## Request lifecycle

What happens when a client sends `SET greeting namaste`:

```
client ──TCP──▶ server.ListenAndServe          accept loop, `go serveConn(conn)`
                    │
                    ▼
                serveConn                      loop until EOF / error / EXIT
                    │  resp.Parser.Parse()     reads one RESP2 value from the socket
                    ▼
                *3\r\n$3\r\nSET\r\n$8\r\ngreeting\r\n$7\r\nnamaste\r\n
                    │  Value.Command()         → ("SET", ["greeting", "namaste"])
                    ▼
                command.Dispatch               case-insensitive lookup in Registry
                    │
                    ▼
                set(store, args, writer)       validates arity, calls store.Set
                    │
                    ▼
                resp.Writer.WriteSimpleString("OK")  ──▶  +OK\r\n
```

Error handling at each step:

| Failure                                     | Result                                                    |
|---------------------------------------------|-----------------------------------------------------------|
| Malformed RESP (bad prefix, bad length)     | Parse error is logged and the connection is closed        |
| Valid RESP but not an array of bulk strings | `-ERR resp: ...` reply, connection stays open             |
| Unknown command                             | `-ERR unknown command '<name>'`, connection stays open    |
| Wrong number of arguments                   | `-ERR wrong number of arguments for '<cmd>' command`      |
| `EXIT`                                      | `+OK`, then the handler returns `command.ErrQuit` and the server closes the connection |
| Socket write fails                          | Handler returns the error and the connection is closed    |

Requests on one connection are handled strictly in order, one at a time. Pipelining works
naturally: the parser just reads the next value once the previous reply has been written.

## Concurrency model

- **Server:** one goroutine per accepted connection. Nothing is shared between connections
  except the store.
- **`store.Store`:** a `map[string]string` behind a `sync.RWMutex`. `GET` and `KEYS` take the read
  lock and `SET` takes the write lock, so every command is atomic with respect to the others.
- **`client.Client`:** a mutex serializes each request/reply pair. This lets one `Client` be shared
  safely across goroutines. The web dashboard relies on this to serve all HTTP requests over a
  single connection.
- **`store.DB` (LSM engine):** writers share a read lock so their WAL appends can be batched by
  group commit. Memtable rotation takes the write lock, and a single background goroutine flushes
  memtables. Details are in [storage-engine.md](storage-engine.md#concurrency).

## Frontends

The CLI (`everestkv-cli`) and the dashboard (`everestkv-web`) contain **no protocol code**. Both
use `internal/client`, which is the only place that builds RESP requests and interprets replies.
`client.FormatReply` renders replies, so the CLI and the dashboard console print identical output.
A new command added on the server therefore works in both frontends immediately, with no frontend
changes.

## Current state vs. target

The storage layer is in the middle of a transition:

- **Today:** `internal/server` constructs `store.New()`, the in-memory map. Data is lost on restart.
- **In progress:** `store.DB` is a working LSM write path (WAL, group commit, memtable, SSTable
  flush, manifest, recovery) with its own tests. It has not been connected to the server yet, and
  its read path does not consult SSTables yet.

Connecting the server to `store.DB` requires, at minimum: using SSTable point reads in `DB.Get`,
a `KEYS` equivalent (an iterator across memtables and SSTables), a data-directory flag, and
calling `DB.Close` after `Server.Shutdown` returns (see [Shutdown](#shutdown)). The roadmap in the [README](../README.md#roadmap) tracks this work.

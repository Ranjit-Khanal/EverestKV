# EverestKV

EverestKV is a Redis-compatible key-value store written in Go, with no dependencies beyond the
standard library. It is built as **local-first infrastructure for Nepali builders**: a small KV
you can read end to end, run on your own VPS or laptop, and fully own. Basic caching and sessions
shouldn't have to depend on a hosted Redis.

It is a systems / core-engineering project: TCP networking, the RESP protocol, concurrency, and a
from-scratch LSM storage engine, all behind clean package boundaries.

> **Status: early development.** It speaks RESP2, so `redis-cli` works, and it supports a small
> string command set. The server currently keeps data **in memory only**. A persistent LSM
> engine (write-ahead log, memtables, SSTables, crash recovery) is being built in
> [`internal/store`](docs/storage-engine.md) but is not yet connected to the server. There is no
> authentication. Do not expose it to the internet ([SECURITY.md](SECURITY.md)).

## Why EverestKV?

Many teams use Redis only for caching, sessions, or OTPs. EverestKV targets that niche, especially
builders in Nepal who want infrastructure they can read, host, and own:

- **One binary, simple deploy:** `make build && ./bin/everestkv`
- **RESP-compatible:** works with `redis-cli` and existing Redis mental models
- **Readable:** small packages, heavily commented invariants, no framework
- **Open source** with an architecture-first codebase for contributors

Compatibility with Redis is a tool for adoption, not a promise to clone every command. We prefer
depth over feature count.

## Quick start

Requires Go 1.26+.

```bash
git clone https://github.com/Ranjit-Khanal/everestkv.git
cd everestkv
make build         # → bin/everestkv, bin/everestkv-cli, bin/everestkv-web

make run           # start the server on :6379
```

In another terminal:

```console
$ make run-cli
Connected to EverestKv at localhost:6379
> SET city Kathmandu
OK
> GET city
Kathmandu
> KEYS *
city
> EXIT
Bye!
```

Or use any Redis client:

```bash
redis-cli -p 6379 SET greeting namaste
redis-cli -p 6379 GET greeting
```

## Commands

| Command          | Reply                                    |
|------------------|------------------------------------------|
| `PING`           | `PONG`                                   |
| `ECHO message`   | `message`                                |
| `SET key value`  | `OK`                                     |
| `GET key`        | value, or nil if missing                 |
| `KEYS *`         | all keys (only the `*` pattern)          |
| `EXIT`           | `OK`, then the server closes the connection |

See [docs/commands.md](docs/commands.md) for exact semantics, error replies, differences from
Redis, and the wire format.

## Tools

| Binary           | Make target    | Purpose                                                        |
|------------------|----------------|----------------------------------------------------------------|
| `everestkv`      | `make run`     | The server (RESP2 over TCP, `:6379`)                           |
| `everestkv-cli`  | `make run-cli` | Interactive prompt (`-addr host:port`)                         |
| `everestkv-web`  | `make run-web` | Browser dashboard and JSON API on `:8080` (`-addr`, `-server`) |

The CLI and the dashboard are thin frontends over the shared `internal/client` package, so they
always support exactly what the server supports. See [docs/web-dashboard.md](docs/web-dashboard.md)
for the dashboard and its HTTP API.

## Architecture

```
 redis-cli / everestkv-cli / everestkv-web
                  │  RESP2 over TCP :6379
                  ▼
 ┌────────────────────────────────────┐
 │ internal/server   accept loop,     │  one goroutine per connection
 │                   session loop     │
 └─────────────────┬──────────────────┘
                   ▼
 ┌────────────────────────────────────┐   ┌──────────────────────────┐
 │ internal/command  registry +       │──▶│ pkg/resp  RESP2 parse /  │
 │                   handlers         │   │           write          │
 └─────────────────┬──────────────────┘   └──────────────────────────┘
                   ▼
 ┌────────────────────────────────────────────────────────────────────┐
 │ internal/store                                                     │
 │   Store  in-memory map + RWMutex             ◀── used by server    │
 │   DB     LSM engine: WAL → memtable → SSTable ◀── in progress      │
 └────────────────────────────────────────────────────────────────────┘
```

Each layer only depends on the one below it: the parser knows nothing about commands, and the
store knows nothing about TCP or RESP. See [docs/architecture.md](docs/architecture.md) for the
request lifecycle and concurrency model.

### Storage engine

`internal/store.DB` is a from-scratch log-structured merge-tree:

- **Write-ahead log**: segmented, CRC-32C checksummed. Writes are acknowledged only after fsync.
- **Group commit**: concurrent writers share one fsync, for higher throughput under load.
- **Skip-list memtable**: rotated at 4 MiB, with bounded backpressure while flushes catch up.
- **SSTables**: sorted, sparse-indexed, and written atomically (temp file + rename + dir fsync).
- **Manifest and recovery**: after a crash, replay only the WAL segments not yet captured by an
  SSTable, safely ignoring torn writes and half-finished flushes.

Formats, invariants and limitations are documented in
[docs/storage-engine.md](docs/storage-engine.md).

## Project layout

```
cmd/everestkv/            server entrypoint
cmd/everestkv/cli/        interactive client
cmd/everestkv/web/        web dashboard (HTTP API + embedded UI)
internal/server/          TCP accept + session loop
internal/command/         command registry and handlers
internal/client/          RESP2 client shared by the CLI and dashboard
internal/store/           in-memory Store + LSM DB engine
  wal/ memtable/ sstable/ manifest/
pkg/resp/                 RESP2 encode/decode (reusable library)
docs/                     design and reference documentation
```

The layout follows [golang-standards/project-layout](https://github.com/golang-standards/project-layout).

## Development

```bash
make build                 # build all three binaries into bin/
make clean                 # remove bin/
go test -race ./...        # run all tests
go test ./internal/store -run '^$' -bench . -cpu 1,4,16   # WAL sync-mode benchmarks
```

## Roadmap

Done:

- [x] RESP2 parser and writer
- [x] TCP server, interactive CLI, web dashboard
- [x] `PING`, `ECHO`, `GET`, `SET`, `KEYS *`, `EXIT`
- [x] LSM write path: WAL, group commit, memtable, SSTable flush, manifest, crash recovery

Next, toward a minimum useful KV:

- [ ] SSTable reads (point lookups through the sparse index)
- [ ] Connect the server to the LSM engine (data directory flag, graceful shutdown)
- [ ] `DEL`, `EXISTS`, `QUIT`
- [ ] TTL: `EXPIRE`, `TTL`, `SET ... EX`
- [ ] Compaction
- [ ] `AUTH` and a configurable listen address
- [ ] Broader Redis-compatible commands

## Documentation

| Document                                           | What's in it                                  |
|----------------------------------------------------|-----------------------------------------------|
| [docs/architecture.md](docs/architecture.md)       | Layers, request lifecycle, concurrency        |
| [docs/commands.md](docs/commands.md)               | Command reference, wire protocol, adding commands |
| [docs/storage-engine.md](docs/storage-engine.md)   | LSM design, on-disk formats, crash recovery   |
| [docs/web-dashboard.md](docs/web-dashboard.md)     | Dashboard usage and HTTP API                  |
| [CONTRIBUTING.md](CONTRIBUTING.md)                 | Workflow, tests, design rules, PR checklist   |
| [SECURITY.md](SECURITY.md)                         | Security model and vulnerability reporting    |

API docs for every package: `go doc ./pkg/resp`, `go doc ./internal/store`, and so on.

## Contributing

Contributions are welcome, especially on the storage engine and protocol. Read
[CONTRIBUTING.md](CONTRIBUTING.md) and open an issue before starting large changes. Together we
can build local-first infrastructure that Nepal actually owns.

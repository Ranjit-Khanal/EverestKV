# EverestKV

EverestKV is an HTTP key-value store written in Go, with no dependencies beyond the
standard library. It is built as **local-first infrastructure for Nepali builders**: a small KV
you can read end to end, run on your own VPS or laptop, and fully own. Basic caching and sessions
shouldn't have to depend on a hosted Redis.

It is a systems / core-engineering project: a small HTTP API, concurrency, and a from-scratch LSM
storage engine, all behind clean package boundaries.

> **Status: early development.** It serves a small HTTP API (`GET`/`PUT`/`DELETE` on
> `/v1/kv/{key}`), so `curl` or any HTTP client works. The server currently keeps data **in memory only**. A persistent LSM
> engine (write-ahead log, memtables, SSTables, crash recovery) is being built in
> [`internal/store`](docs/storage-engine.md) but is not yet connected to the server. There is no
> authentication. Do not expose it to the internet ([SECURITY.md](SECURITY.md)).

## Why EverestKV?

Many teams use Redis only for caching, sessions, or OTPs. EverestKV targets that niche, especially
builders in Nepal who want infrastructure they can read, host, and own:

- **One binary, simple deploy:** `make build && ./bin/everestkv`
- **Plain HTTP API:** works with `curl` and the HTTP client in any language, with no driver needed
- **Readable:** small packages, heavily commented invariants, no framework
- **Open source** with an architecture-first codebase for contributors

We prefer depth over feature count.

## Quick start

Requires Go 1.26+.

```bash
git clone https://github.com/Ranjit-Khanal/everestkv.git
cd everestkv
make build         # → bin/everestkv, bin/everestkv-cli, bin/everestkv-web

make run           # start the server on :8379
```

In another terminal:

```console
$ make run-cli
Connected to EverestKv at localhost:8379
> SET city Kathmandu
OK
> GET city
Kathmandu
> KEYS *
city
> EXIT
Bye!
```

Or use any HTTP client:

```bash
curl -X PUT --data-binary namaste localhost:8379/v1/kv/greeting
curl localhost:8379/v1/kv/greeting
```

## HTTP API

| Request                | Response                                   |
|------------------------|--------------------------------------------|
| `GET /v1/ping`         | `200 PONG`                                 |
| `PUT /v1/kv/{key}`     | `204`; the request body is the value       |
| `GET /v1/kv/{key}`     | `200` with the raw value, or `404`         |
| `DELETE /v1/kv/{key}`  | `204`, or `404` if the key did not exist   |
| `GET /v1/keys`         | `200 {"keys": [...]}`, sorted              |

The CLI accepts `PING`, `SET`, `GET`, `DEL`, `KEYS *` and `EXIT` and turns each one into one of
these requests. See [docs/commands.md](docs/commands.md) for key encoding, status codes, limits,
and the CLI commands.

## Tools

| Binary           | Make target    | Purpose                                                        |
|------------------|----------------|----------------------------------------------------------------|
| `everestkv`      | `make run`     | The server (HTTP API on `:8379`)                               |
| `everestkv-cli`  | `make run-cli` | Interactive prompt (`-addr host:port`)                         |
| `everestkv-web`  | `make run-web` | Browser dashboard and JSON API on `:8080` (`-addr`, `-server`) |

The CLI and the dashboard are thin frontends over the shared `internal/client` package, so they
always support exactly what the server supports.

## Web dashboard: step by step

The dashboard is a browser UI for EverestKV. It stores nothing itself. Every click is sent to the
server's HTTP API, just as if you had typed the command in the CLI.

**1. Build the binaries**

```bash
make build
```

This produces `bin/everestkv` (server) and `bin/everestkv-web` (dashboard).

**2. Start the server** (terminal 1)

```bash
make run
```

Wait for `everestkv listening on [::]:8379`. The dashboard checks the server at startup and exits
if the server isn't running, so always start the server first.

**3. Start the dashboard** (terminal 2)

```bash
make run-web
```

You should see:

```
everestkv dashboard on http://localhost:8080 (backing store: localhost:8379)
```

To change the ports or keep the dashboard reachable only from your machine, run the binary
directly with flags:

```bash
./bin/everestkv-web -addr 127.0.0.1:9090 -server localhost:8379
```

| Flag      | Default          | Meaning                                                    |
|-----------|------------------|------------------------------------------------------------|
| `-addr`   | `:8080`          | Where the dashboard listens (the default is all interfaces) |
| `-server` | `localhost:8379` | Which EverestKV server to talk to                          |

**4. Open it in your browser**

Go to <http://localhost:8080>. The dot in the top-right corner turns green once the dashboard
can reach the server. It is re-checked every 5 seconds.

**5. Store and read a value (Key / Value panel)**

- Enter a key and a value, for example `city` / `Kathmandu`, and click **SET**.
- Enter `city` in the GET box and click **GET** to read it back. A missing key shows as not found.

**6. Browse everything (Stored Keys panel)**

This panel lists every key with its value, sorted by key. It refreshes every 5 seconds; click
**Refresh** to update it immediately. It is designed for small datasets, because it runs one
`GET` per key.

**7. Run any command (Console panel)**

Type any command the CLI accepts, such as `PING`, `KEYS *`, `GET city` or `DEL city`, and
click **Run**. The output looks the same as in `everestkv-cli`. `EXIT` and `QUIT` are blocked here
because they only apply to the CLI prompt.

**8. (Optional) Script it with the JSON API**

```bash
curl localhost:8080/api/status
curl -X POST localhost:8080/api/set -d '{"key":"city","value":"Kathmandu"}'
curl 'localhost:8080/api/get?key=city'
curl localhost:8080/api/keys
curl -X POST localhost:8080/api/command -d '{"line":"PING"}'
```

**9. Stop it**

Press `Ctrl+C` in each terminal. The dashboard keeps working if the server restarts; it just
shows errors while the server is down.

> **Troubleshooting**
> - `connecting to EverestKV at localhost:8379: ... connect: connection refused`: the server isn't running.
>   Do step 2 first.
> - `listen tcp :8080: bind: address already in use`: another program is using the port. Pick
>   a different one with `-addr :9090`.
> - The status dot is red, or requests return `502`: the server stopped. Restart the server.
>
> ⚠️ The dashboard has no login. Don't expose it to the internet; see [SECURITY.md](SECURITY.md).

Full endpoint reference and response formats: [docs/web-dashboard.md](docs/web-dashboard.md).

## Architecture

```
 curl / everestkv-cli / everestkv-web
                  │  HTTP :8379
                  ▼
 ┌────────────────────────────────────┐
 │ internal/server   net/http routes  │  one goroutine per connection
 │                   + handlers       │
 └─────────────────┬──────────────────┘
                   ▼
 ┌────────────────────────────────────────────────────────────────────┐
 │ internal/store                                                     │
 │   Store  in-memory map + RWMutex             ◀── used by server    │
 │   DB     LSM engine: WAL → memtable → SSTable ◀── in progress      │
 └────────────────────────────────────────────────────────────────────┘
```

Each layer only depends on the one below it: the store knows nothing about HTTP. See [docs/architecture.md](docs/architecture.md) for the
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
internal/server/          HTTP API: routes, handlers, graceful shutdown
internal/client/          HTTP client shared by the CLI and dashboard
internal/store/           in-memory Store + LSM DB engine
  wal/ memtable/ sstable/ manifest/
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

- [x] HTTP API server, interactive CLI, web dashboard
- [x] Get, put, delete, list keys
- [x] LSM write path: WAL, group commit, memtable, SSTable flush, manifest, crash recovery
- [x] Graceful shutdown on SIGINT/SIGTERM
- [x] SSTable reads: `DB.Get` checks memtables, then SSTables newest to oldest

Next, toward a minimum useful KV:

- [ ] Connect the server to the LSM engine (data directory flag, `DB.Close` on shutdown)
- [ ] TTL on keys
- [ ] Compaction
- [ ] Authentication, TLS, and a configurable listen address

## Documentation

| Document                                           | What's in it                                  |
|----------------------------------------------------|-----------------------------------------------|
| [docs/architecture.md](docs/architecture.md)       | Layers, request lifecycle, concurrency        |
| [docs/commands.md](docs/commands.md)               | HTTP API and CLI command reference, adding endpoints |
| [docs/storage-engine.md](docs/storage-engine.md)   | LSM design, on-disk formats, crash recovery   |
| [docs/web-dashboard.md](docs/web-dashboard.md)     | Dashboard usage and HTTP API                  |
| [CONTRIBUTING.md](CONTRIBUTING.md)                 | Workflow, tests, design rules, PR checklist   |
| [SECURITY.md](SECURITY.md)                         | Security model and vulnerability reporting    |

API docs for every package: `go doc ./internal/server`, `go doc ./internal/store`, and so on.

## Contributing

Contributions are welcome, especially on the storage engine and API. Read
[CONTRIBUTING.md](CONTRIBUTING.md) and open an issue before starting large changes. Together we
can build local-first infrastructure that Nepal actually owns.

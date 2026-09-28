# Web dashboard

`everestkv-web` is a small HTTP server that gives EverestKV a browser UI and a JSON API. It stores
nothing itself. Every request becomes a call to the server's HTTP API, made through the same
`internal/client` package the CLI uses. The dashboard's `/api/*` routes are a convenience layer
for the browser UI; other programs can call the server's [HTTP API](commands.md) directly.

```bash
make run        # terminal 1: server on :8379
make run-web    # terminal 2: dashboard on :8080
```

Open <http://localhost:8080>.

## Flags

| Flag      | Default          | Description                          |
|-----------|------------------|--------------------------------------|
| `-addr`   | `:8080`          | HTTP listen address. The default binds **all interfaces**; use `127.0.0.1:8080` to keep it local. |
| `-server` | `localhost:8379` | EverestKV server to connect to       |

The dashboard pings the server at startup and exits if it can't reach it. After that, each
request is independent, so a server restart doesn't require restarting the dashboard.

## UI

- **Status**: pings the server and shows whether the connection is healthy.
- **Key / Value**: GET and SET forms.
- **Stored Keys**: table of every key and its value, refreshed automatically. It lists the keys
  and then makes one `GET` per key, so it is intended for small datasets.
- **Console**: runs any command line exactly as the CLI does. `EXIT` and `QUIT` are rejected,
  because they only apply to the CLI prompt.

The UI files (`cmd/everestkv/web/static/`) are embedded with `go:embed`, so the binary needs no
extra files at runtime.

## HTTP API

All responses are JSON. Errors have the form `{"error": "message"}`.

### `GET /api/status`

```json
{"connected": true, "server": "localhost:8379", "reply": "PONG"}
```

This always returns `200`. When the server is unreachable, `connected` is `false` and an `error`
field is included.

### `GET /api/get?key=<key>`

```json
{"key": "city", "value": "Kathmandu", "found": true}
```

A missing key returns `200` with `"found": false`. It is not treated as an error.

### `POST /api/set`

```bash
curl -X POST localhost:8080/api/set -d '{"key":"city","value":"Kathmandu"}'
```

```json
{"ok": true}
```

### `GET /api/keys`

```json
{"entries": [{"key": "city", "value": "Kathmandu"}]}
```

Entries are sorted by key.

### `POST /api/command`

```bash
curl -X POST localhost:8080/api/command -d '{"line":"GET city"}'
```

```json
{"reply": "Kathmandu", "isError": false}
```

`reply` is formatted the same way the CLI prints it (`(nil)`, `(empty array)`, one array element
per line). A rejected command line or an error from the server comes back as `200` with
`"isError": true`.

### Status codes

| Code  | When                                                         |
|-------|--------------------------------------------------------------|
| `400` | Missing `key`, invalid JSON body, or `EXIT`/`QUIT` in console |
| `405` | Wrong HTTP method                                            |
| `502` | Connection error, or an error response from the server (`/api/get`, `/api/set`, `/api/keys`) |

## Security

The dashboard has **no authentication** and can run arbitrary commands. Run it with
`-addr 127.0.0.1:8080`, or put it behind an authenticating reverse proxy. See [SECURITY.md](../SECURITY.md).

# HTTP API and commands

EverestKV serves a small HTTP API. It listens on port `8379` by default. Any HTTP client works,
including `curl`.

## HTTP API

| Method   | Path           | Success                                  | Failure |
|----------|----------------|------------------------------------------|---------|
| `GET`    | `/v1/ping`     | `200`, body `PONG`                        |         |
| `GET`    | `/v1/keys`     | `200`, `{"keys": ["a", "b"]}`, sorted     |         |
| `GET`    | `/v1/kv/{key}` | `200`, the raw value as the body (`application/octet-stream`) | `404` if the key is missing |
| `HEAD`   | `/v1/kv/{key}` | `200` if the key exists                   | `404`   |
| `PUT`    | `/v1/kv/{key}` | `204`. The request body is the value, stored as-is. Overwrites any existing value. | `413` if the body is over 32 MiB |
| `DELETE` | `/v1/kv/{key}` | `204`                                     | `404` if the key did not exist |

- **Keys** are everything after `/v1/kv/`, percent-decoded. A key can contain any byte, including
  `/`, spaces and `?`, as long as the client percent-encodes it (for example, key `a/b` is
  `/v1/kv/a%2Fb`). The path is not cleaned, so `a//b` and `x/../y` are stored as written. The
  empty key is rejected with `400`.
- **Values** are raw bytes, not JSON, so binary data round-trips unchanged.
- **Errors** are JSON: `{"error": "key not found"}`. Other codes: `400` for a malformed key
  escape, `405` (with an `Allow` header) for an unsupported method, `404` for unknown paths.

### Examples

```console
$ curl -X PUT --data-binary 'Kathmandu' localhost:8379/v1/kv/city
$ curl localhost:8379/v1/kv/city
Kathmandu
$ curl -i localhost:8379/v1/kv/missing
HTTP/1.1 404 Not Found
...
{"error":"key not found"}
$ curl localhost:8379/v1/keys
{"keys":["city"]}
$ curl -X PUT --data-binary @photo.jpg localhost:8379/v1/kv/photos%2Fnepal.jpg
$ curl -X DELETE localhost:8379/v1/kv/city
```

Use `--data-binary`, not `-d`. `curl -d` strips newlines from the value.

### Limitations

- There is no TTL, no conditional write (`If-Match`), no pattern matching on `/v1/keys`, and no
  authentication. See [SECURITY.md](../SECURITY.md).
- Data is held in memory and is **lost when the server restarts**. See
  [storage-engine.md](storage-engine.md) for the persistent engine in progress.

## CLI commands

`everestkv-cli` and the dashboard console accept Redis-style command lines and turn each one into
one HTTP request. Command names are case-insensitive.

| Command | Syntax          | Request                  | Output |
|---------|-----------------|--------------------------|--------|
| `PING`  | `PING`          | `GET /v1/ping`           | `PONG` |
| `SET`   | `SET key value` | `PUT /v1/kv/key`         | `OK` |
| `GET`   | `GET key`       | `GET /v1/kv/key`         | The value, or `(nil)` if the key is missing |
| `DEL`   | `DEL key`       | `DELETE /v1/kv/key`      | `1` if the key existed, otherwise `0` |
| `KEYS`  | `KEYS *`        | `GET /v1/keys`           | One key per line, sorted, or `(empty array)`. Only the literal pattern `*` is accepted. |
| `EXIT` / `QUIT` | `EXIT`  | none                     | Leaves the CLI without contacting the server |

Bad command lines are rejected by the client before anything is sent:

```
ERR unknown command 'foo'
ERR wrong number of arguments for 'get' command
ERR KEYS only supports the '*' pattern for now
```

Each line is split on whitespace, so a key or value containing spaces can't be set from the CLI.
Use `curl` for that.

```console
$ ./bin/everestkv-cli
Connected to EverestKv at localhost:8379
> SET city Kathmandu
OK
> GET city
Kathmandu
> DEL city
1
> EXIT
Bye!
```

## Adding an operation

1. If it needs new storage behavior, add a method to the store. Handlers never touch the store's
   internals.
2. Add the route in `internal/server/server.go` (register it on the mux in `New`, or extend
   `handleKV` for a new method on a key). Use proper status codes and `writeError` for failures.
3. Add a method to `internal/client` that makes the request, and, if it should be usable from the
   CLI and dashboard console, a case in `Client.Execute`.
4. Add tests in `internal/server/server_test.go`.
5. Update the tables above.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the full checklist.

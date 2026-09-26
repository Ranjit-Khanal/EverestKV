# Commands and protocol

EverestKV speaks **RESP2**, the Redis serialization protocol, over TCP. It listens on port `6379`
by default. Any Redis client or `redis-cli` can connect.

## Command reference

Command names are case-insensitive. Keys and values are binary-safe strings.

| Command | Syntax           | Reply                                              | Notes |
|---------|------------------|----------------------------------------------------|-------|
| `PING`  | `PING`           | Simple string `PONG`                               | Any arguments are ignored (Redis would echo them). |
| `ECHO`  | `ECHO message`   | Bulk string `message`                              | Exactly one argument. |
| `SET`   | `SET key value`  | Simple string `OK`                                 | Overwrites any existing value. Options such as `EX`, `PX`, `NX`, `XX` are **not** supported: the command takes exactly two arguments. |
| `GET`   | `GET key`        | Bulk string, or null bulk (`$-1`) if the key is missing |       |
| `KEYS`  | `KEYS *`         | Array of bulk strings, in no particular order      | Only the literal pattern `*` is accepted. Glob patterns return an error. |
| `EXIT`  | `EXIT`           | Simple string `OK`, then the server closes the connection |  |

### Errors

All errors are RESP error replies (`-ERR ...`). They leave the connection open:

```
-ERR unknown command 'foo'
-ERR wrong number of arguments for 'get' command
-ERR KEYS only supports the '*' pattern for now
```

### Differences from Redis

- **`QUIT` is not implemented on the server.** Use `EXIT`. (`redis-cli` handles `quit` locally,
  so typing it there still works.)
- There is no TTL, no `DEL`, no data types other than strings, and no `AUTH`, `SELECT`, `INFO`,
  `CONFIG`, `CLIENT` or `COMMAND`. Some Redis client libraries send one of these on connect;
  it gets `-ERR unknown command`.
- There are no inline commands. Requests must be RESP arrays of bulk strings, which is what every
  real client sends. Typing raw text into `nc` / `telnet` will not work.
- Data is held in memory and is **lost when the server restarts**. See
  [storage-engine.md](storage-engine.md) for the persistent engine in progress.

## Example session

With `redis-cli`:

```console
$ redis-cli -p 6379
127.0.0.1:6379> PING
PONG
127.0.0.1:6379> SET city Kathmandu
OK
127.0.0.1:6379> GET city
"Kathmandu"
127.0.0.1:6379> GET missing
(nil)
127.0.0.1:6379> KEYS *
1) "city"
```

With the bundled CLI:

```console
$ ./bin/everestkv-cli
Connected to EverestKv at localhost:6379
> SET city Kathmandu
OK
> GET city
Kathmandu
> EXIT
Bye!
```

`everestkv-cli` splits each line on whitespace, so a value containing spaces cannot be set from
it. Use `redis-cli` with quotes for that. `EXIT` in the CLI is handled locally and quits the
program without contacting the server.

## Wire format

Every request is an array of bulk strings. `SET city Kathmandu` is sent as:

```
*3\r\n
$3\r\nSET\r\n
$4\r\ncity\r\n
$9\r\nKathmandu\r\n
```

The reply types the server uses:

| Prefix | Type          | Example                     |
|--------|---------------|-----------------------------|
| `+`    | Simple string | `+OK\r\n`                   |
| `-`    | Error         | `-ERR unknown command 'x'\r\n` |
| `:`    | Integer       | `:42\r\n` (not used by any command yet) |
| `$`    | Bulk string   | `$9\r\nKathmandu\r\n`, null: `$-1\r\n` |
| `*`    | Array         | `*1\r\n$4\r\ncity\r\n`      |

Requests are processed in order per connection, so pipelining (sending several requests before
reading replies) works.

You can try the raw protocol with `printf` and `nc`:

```bash
printf '*1\r\n$4\r\nPING\r\n*2\r\n$3\r\nGET\r\n$4\r\ncity\r\n' | nc localhost 6379
```

## Adding a command

1. Write a handler in `internal/command/`, in the file for its family (`string.go`, `keys.go`,
   `connection.go`, or a new one):

   ```go
   func strlen(s *store.Store, args []string, w *resp.Writer) error {
       if len(args) != 1 {
           return w.WriteError("ERR wrong number of arguments for 'strlen' command")
       }
       v, _ := s.Get(args[0])
       return w.Write(resp.Value{Type: resp.TypeInteger, Int: int64(len(v))})
   }
   ```

2. Register it in `Registry` in `internal/command/registry.go`: `"STRLEN": strlen,`.
3. If it needs new storage behavior, add a method to the store. Handlers never touch the store's
   internals.
4. Match Redis's reply type and error text wherever you claim compatibility.
5. Update the table above.

Server, CLI and dashboard need no changes. See [CONTRIBUTING.md](../CONTRIBUTING.md) for the full
checklist.

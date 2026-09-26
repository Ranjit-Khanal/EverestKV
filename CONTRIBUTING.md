# Contributing to EverestKV

Thanks for your interest. EverestKV is a **systems / core-engineering** project. We prefer
contributions that improve architecture, correctness, performance, or the clarity of the hot path
over surface-level features.

New here? Read [docs/architecture.md](docs/architecture.md) first. It is short and explains where
everything lives.

## What we value

1. **Architecture first**: clear package boundaries (`cmd` / `internal` / `pkg`), no god packages,
   no RESP details leaking into the store and no store details leaking into the parser.
2. **Core engineering**: networking, protocol framing, concurrency safety, the memory model, and
   persistence design. Feature work should build on those foundations.
3. **Small, reviewable changes**: one concern per PR. A solid `SET` beats ten unfinished commands.
4. **Readable Go**: simple control flow, explicit errors, and names that match Redis semantics
   wherever we claim compatibility.
5. **Evidence**: back up behavior or performance claims with a test, a benchmark, or a short
   design note in the PR.

## What we are cautious about

- Large dependency additions (the module currently has none)
- Protocol changes that break RESP clients without a strong reason
- Premature abstraction (frameworks, plugin systems, heavy interfaces)
- Drive-by refactors unrelated to the PR's goal
- Features without a clear place in the architecture (transport → protocol → dispatch → store)

## Getting started

Requirements: Go **1.26+** (see `go.mod`), `make`, and optionally `redis-cli` for manual testing.

```bash
git clone https://github.com/Ranjit-Khanal/everestkv.git
cd everestkv
make build
make run        # terminal 1: server on :6379
make run-cli    # terminal 2: interactive client
```

## Development workflow

1. **Open an issue first** for non-trivial work (new commands, store design, persistence) so we
   agree on direction before you invest time.
2. Branch from `main`.
3. Keep commits focused, and follow the style of neighboring files.
4. Before pushing, run:

   ```bash
   gofmt -l .                  # must print nothing
   go vet ./...
   go test -race ./...
   make build
   ```

5. In the PR, explain **why**: architectural impact, tradeoffs, and how to verify.

## Tests

- Tests live next to the code (`*_test.go`). The storage engine (`internal/store/...`) has the
  most thorough coverage, including crash-recovery and concurrency tests. Mirror those when you
  change it.
- Storage and concurrency changes **must** come with tests, and must pass under `-race`.
- For crash-safety changes, write a test that simulates the crash point (see
  `TestCrashMidFlushIgnoresPartialSSTable` in `internal/store/db_test.go`).
- Performance claims need benchmark numbers from before and after:

  ```bash
  go test ./internal/store -run '^$' -bench . -cpu 1,4,16
  ```

- For command and protocol changes, also check manually with `make run` plus `make run-cli` or
  `redis-cli`.

## Design guidelines

| Layer               | Owns                         | Must not own            |
|---------------------|------------------------------|-------------------------|
| `cmd/*`             | Wiring `main`                | Business logic          |
| `internal/server`   | Connections, session loop    | Command implementations |
| `internal/command`  | Handlers + registry          | TCP accept loop         |
| `internal/client`   | Client-side RESP requests    | Storage, HTTP           |
| `pkg/resp`          | RESP parse/encode            | Commands, storage       |
| `internal/store`    | Keys, values, TTL, durability| TCP or RESP             |

To add a command, follow the walkthrough in [docs/commands.md](docs/commands.md#adding-a-command).
In short:

1. Add a handler under `internal/command/` and register it in `Registry`.
2. Mutate and query state only through the store API.
3. Reply with the correct RESP type (`+`, `-`, `$`, `:`, `*`), matching Redis.
4. Update the command table in `docs/commands.md`.

For storage engine work, read [docs/storage-engine.md](docs/storage-engine.md). Any change to an
on-disk format or to the recovery order must keep the crash-safety invariants listed there, and
must update that document.

## Code style

- Format with `gofmt`.
- Prefer the standard library, and justify any new module.
- Every package has a package comment, and every exported identifier has a doc comment. Check
  with `go doc ./<pkg>`.
- Comments explain non-obvious invariants (locking, ordering, durability), not what the code
  already says.
- Error messages are prefixed with the package name (`store: ...`, `wal: ...`) and wrap causes
  with `%w`.

## Documentation

Behavior changes are only complete when the docs are updated as well:

| You changed…                 | Update                                     |
|------------------------------|--------------------------------------------|
| A command or reply           | `docs/commands.md`                         |
| Dashboard or its HTTP API    | `docs/web-dashboard.md`                    |
| Package layout or layering   | `docs/architecture.md`, README layout      |
| Storage formats or recovery  | `docs/storage-engine.md`                   |
| Roadmap item finished        | README "Status" / "Roadmap"                |

## Pull request checklist

- [ ] The change has a clear architectural home
- [ ] `gofmt -l .` is clean, and `go vet ./...` and `go test -race ./...` pass
- [ ] Tests added or updated for behavior changes
- [ ] Docs updated (see the table above)
- [ ] The PR description covers motivation, design, and verification
- [ ] Scope stays small; follow-ups are called out instead of bundled

## Reporting bugs

Open an issue with the Go version, OS, the exact commands you ran, what you expected, and what
happened. For security issues, follow [SECURITY.md](SECURITY.md) instead. Do not file them
publicly.

## Communication

Be direct and technical. Disagreement is welcome when it is about correctness, safety, or
structure. If a proposal conflicts with the layered design above, say so early and suggest an
alternative that keeps the boundaries intact.

## License

By contributing, you agree that your contributions will be licensed under the same license as
the repository.

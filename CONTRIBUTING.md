# Contributing to EverestKV

Thanks for taking an interest. EverestKV is a **systems / core-engineering** project. Contributions that improve architecture, correctness, performance, or clarity of the hot path are preferred over surface-level features.

## What we value

1. **Architecture first** — clear package boundaries (`cmd` / `internal` / `pkg`), no god packages, no leaking RESP details into the store (or store details into the parser).
2. **Core engineering** — networking, protocol framing, concurrency safety, memory model, persistence design. Feature work should sit on those foundations.
3. **Small, reviewable changes** — one concern per PR; prefer a solid `SET` over ten unfinished commands.
4. **Readable Go** — simple control flow, explicit errors, names that match Redis semantics when we claim compatibility.
5. **Evidence** — for behavior or performance claims, show a short repro, benchmark, or design note in the PR.

## What we are cautious about

- Large dependency additions
- Protocol changes that break RESP clients without a strong reason
- Premature abstraction (frameworks, plugin systems, heavy interfaces)
- Drive-by refactors unrelated to the PR’s goal
- Features without a clear place in the architecture (transport → protocol → dispatch → store)

## Getting started

```bash
git clone https://github.com/Ranjit-Khanal/everestkv.git
cd everestkv
make build
make run        # terminal 1
make run-cli    # terminal 2
```

Requires a recent Go toolchain (see `go.mod`).

## Development workflow

1. Open an issue for non-trivial work (new commands, store design, persistence) so direction stays aligned.
2. Branch from `main` (or the active feature branch agreed in the issue).
3. Keep commits focused; follow existing style in neighboring files.
4. Build with `make build` before opening a PR.
5. Describe **why** in the PR: architectural impact, tradeoffs, and how to verify.

## Design guidelines

| Layer | Owns | Must not own |
|-------|------|----------------|
| `cmd/*` | Wiring `main` | Business logic |
| `internal/server` | Connections, dispatch | Wire encoding details beyond calling `pkg/resp` |
| `pkg/resp` | RESP parse/encode | Commands, storage |
| `internal/store` *(planned)* | Keys, values, TTL, durability | TCP or RESP |

When adding a command:

1. Parse via existing RESP types.
2. Dispatch in `internal/server` (or a dedicated command package under `internal/` if it grows).
3. Mutate/query state only through the store API once it exists.
4. Reply with the correct RESP type (`+`, `-`, `$`, `:`, `*`).

## Code style

- Match existing formatting (`gofmt` / `go fmt ./...`).
- Prefer standard library; justify any new module.
- No drive-by test files unless the PR specifically needs them and maintainers agree.
- Comments explain non-obvious invariants, not what the code already says.

## Pull request checklist

- [ ] Change has a clear architectural home
- [ ] `make build` succeeds
- [ ] Manual check with `make run` + `make run-cli` (or `redis-cli`) when behavior changes
- [ ] PR description covers motivation, design, and verification
- [ ] Scope stays small; follow-ups called out instead of bundled

## Communication

Be direct and technical. Disagreement is fine when it is about correctness, safety, or structure. If a proposal conflicts with the layered design above, say so early and propose an alternative that keeps boundaries intact.

## License

By contributing, you agree that your contributions are licensed under the same terms as the repository (add or update a `LICENSE` if/when one is adopted).

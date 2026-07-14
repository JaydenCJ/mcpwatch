# Contributing to mcpwatch

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — no external services, no network.

```bash
git clone https://github.com/JaydenCJ/mcpwatch && cd mcpwatch
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the CLI plus the demo MCP server, dumps and
diffs real capability surfaces, and drives a live watch session through
an actual file edit; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (91 deterministic tests, no network, no sleeps).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (only `runner` spawns processes, only `watch.Scan` touches
   the filesystem — everything else operates on values).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR. The `require` list in `go.mod` is intentionally empty.
- No network calls, ever — mcpwatch talks only to the local server
  process it spawned. No telemetry.
- Determinism first: identical inputs must produce byte-identical dumps
  and diffs, including all orderings. Tests may not sleep to
  synchronize; timing-dependent logic must be a pure state machine
  (see `watch.Debouncer`).
- Protocol tolerance is a feature: servers under active development
  misbehave, and mcpwatch must degrade to a readable error, never hang
  or crash the dev loop.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `mcpwatch version`, the full command you ran,
what mcpwatch printed (the `[mcpwatch]` lines and any `[server]`
stderr), and — for protocol issues — a transcript of the server's
stdout if you can capture one, since that is exactly what the client
parses.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.

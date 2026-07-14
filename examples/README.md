# mcpwatch examples

Everything here runs offline with nothing but a Go toolchain.

## The demo server

`demoserver/` is a real MCP stdio server whose entire capability
surface lives in a JSON spec file — `notes-server.json` gives it two
tools, a resource, a resource template, and a prompt. It answers
`initialize`, `ping`, all four list methods (with cursor pagination
when the spec sets `pageSize`), and a working `tools/call` for `echo`.

```bash
go build -o demoserver ./examples/demoserver
go build -o mcpwatch  ./cmd/mcpwatch
./mcpwatch dump -- ./demoserver examples/notes-server.json
```

## A complete dev-loop session

Copy the spec somewhere writable and watch it:

```bash
mkdir -p /tmp/mcpwatch-demo
cp examples/notes-server.json /tmp/mcpwatch-demo/caps.json
./mcpwatch run --watch /tmp/mcpwatch-demo -- ./demoserver /tmp/mcpwatch-demo/caps.json
```

Now edit `/tmp/mcpwatch-demo/caps.json` in another terminal — add a
tool, rename a prompt argument, change a schema — and save. mcpwatch
restarts the server and prints exactly what changed. Break the JSON on
purpose to see the failure path: the reload fails with the server's
stderr, and the next successful save diffs against the last *good*
surface.

## Gating surface changes in a script

`diff` exits 1 when two dumps differ, so a baseline check is three
lines:

```bash
./mcpwatch dump --format json -- ./demoserver examples/notes-server.json > baseline.json
# … change the server …
./mcpwatch dump --format json -- ./demoserver examples/notes-server.json | ./mcpwatch diff baseline.json -
```

## Pointing mcpwatch at your own server

Any stdio MCP server works — the command after `--` is spawned as-is:

```bash
./mcpwatch run --watch src --include '*.py' -- python3 -m my_mcp_server
./mcpwatch run --watch . --exclude 'testdata/**' -- node build/server.js
./mcpwatch run --watch cmd --watch internal -- go run ./cmd/my-server
```

For interpreted servers, watch the source tree. For compiled servers,
`go run` / `cargo run` style commands rebuild on each restart, so the
edit → reload → diff loop still holds.

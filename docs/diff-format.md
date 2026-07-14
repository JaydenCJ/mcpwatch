# Capability snapshots and diffs

This document specifies the two machine-facing formats mcpwatch emits:
the **snapshot** (`mcpwatch dump --format json`, also `run --dump-file`)
and the **diff** (`mcpwatch diff --format json`). Both are stable within
a `schema_version`; a field changing meaning bumps the version.

## Snapshot (`schema_version: 1`)

A snapshot is the normalized public surface of one server run:

```json
{
  "schema_version": 1,
  "protocolVersion": "2025-03-26",
  "serverInfo": { "name": "demo-notes", "version": "1.0.0" },
  "toolsSupported": true,
  "tools": [
    {
      "name": "echo",
      "description": "Echo a message back verbatim",
      "inputSchema": {"properties":{"text":{"type":"string"}},"required":["text"],"type":"object"},
      "schemaHash": "e3dce8d1afb8"
    }
  ],
  "resourcesSupported": true,
  "resources": [ { "uri": "notes://today", "name": "Today's notes", "mimeType": "text/markdown" } ],
  "resourceTemplates": [ { "uriTemplate": "notes://{date}", "name": "Notes by date", "mimeType": "text/markdown" } ],
  "promptsSupported": true,
  "prompts": [ { "name": "summarize", "arguments": [ { "name": "date", "required": true } ] } ]
}
```

Normalization rules (what makes two snapshots comparable):

1. **Sorted lists** — tools and prompts by `name`, resources by `uri`,
   templates by `uriTemplate`. The server's listing order carries no
   meaning and is erased.
2. **Canonical schemas** — every `inputSchema` is re-encoded with
   object keys sorted and insignificant whitespace removed; numbers are
   preserved verbatim (no float round-tripping).
3. **Schema hashes** — `schemaHash` is the first 12 hex characters of
   the SHA-256 of the canonical schema bytes. Two schemas hash equal
   iff they are semantically identical JSON.
4. **Support flags** — `toolsSupported: false` means the server did not
   declare the section (or answered `-32601`); an empty array with the
   flag `true` means "declared, currently empty". The two states diff
   differently on purpose.

The same surface always encodes to byte-identical JSON, so snapshots
can be committed, compared with plain `diff`, or hashed.

## Diff

`mcpwatch diff --format json` emits one object with `server` and
`protocol` field changes (present only when changed) and one section
object per capability list:

| Key | Meaning |
|---|---|
| `oldCount` / `newCount` | list sizes on each side |
| `gainedSupport` / `lostSupport` | the whole section appeared/vanished |
| `added[]` | `{name, note}` — note is the description (tools/prompts) or name (resources) |
| `removed[]` | `{name}` |
| `changed[]` | `{name, details[]}` — one human-readable detail per changed aspect |

`details` values are stable strings such as `description changed`,
`input schema changed (2725bb191bef → 0d2ffb730518)`,
`input schema added (…)`, `input schema removed`,
`mimeType changed (text/plain → text/markdown)`, and
`arguments changed (path → path, style?)` (a `?` marks an optional
prompt argument).

In text mode the same data renders as `+` (added), `~` (changed) and
`-` (removed) lines under a `tools 2 → 3` header. Exit codes: `0` when
the surfaces are identical, `1` when they differ, `2`/`3` for
usage/runtime errors — so `mcpwatch diff` works as a CI-style gate:

```bash
mcpwatch dump --format json -- python3 -m my_server > baseline.json
# … refactor …
mcpwatch dump --format json -- python3 -m my_server | mcpwatch diff baseline.json -
```

## What mcpwatch speaks on the wire

mcpwatch is an MCP *client* over stdio: newline-delimited JSON-RPC 2.0,
UTF-8, one message per line. Per (re)start it performs `initialize`
(requesting `2025-03-26` by default; override with `--proto`), sends
`notifications/initialized`, then pages through `tools/list`,
`resources/list`, `resources/templates/list`, and `prompts/list` with
cursors. Sections the server does not declare are never called.
Server-initiated requests (e.g. sampling) are answered with
`-32601` so well-behaved servers proceed; notifications are consumed.
Every call is bounded by `--timeout`, and pagination is capped at 100
pages, so a wedged server cannot wedge the dev loop.

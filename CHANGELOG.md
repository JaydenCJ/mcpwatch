# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- `run` subcommand: the dev loop — poll-based file watching with
  content hashing (byte-identical rewrites don't restart), a settle
  debouncer that folds bursts of saves into one restart, clean process
  shutdown (close stdin → SIGTERM → SIGKILL, process-group aware), and
  a capability diff printed after every reload against the last good
  snapshot (failed reloads keep the baseline).
- `dump` subcommand: one-shot MCP handshake and capability dump as
  aligned terminal text or stable JSON (`schema_version: 1`), with
  cursor pagination, per-call timeouts, and "declared but method not
  found" tolerance for servers mid-refactor.
- `diff` subcommand: compare two dumps offline, exit 1 on differences —
  a surface-change gate for scripts; text and JSON output.
- Semantic capability diffing for tools (description + canonicalized
  input-schema hash), resources (by URI), resource templates, and
  prompts (argument signatures), plus server-info, protocol-version,
  and section-support transitions.
- Snapshot canonicalization: sorted lists, key-sorted schema JSON with
  numbers preserved verbatim, 12-hex-char schema hashes.
- `--include`/`--exclude` globs (`*`, `?`, `**`, basename patterns)
  with sensible default excludes (`.git`, `node_modules`, `*.log`, …).
- Server stderr forwarded live with a `[server] ` prefix (opt out with
  `--quiet-server`); crash reports include the exit state.
- `examples/demoserver`, a runnable spec-driven MCP stdio server used
  by the docs, the smoke test, and the test suite.
- 91 deterministic offline tests (unit + in-process CLI integration
  against a real subprocess server) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/mcpwatch/releases/tag/v0.1.0

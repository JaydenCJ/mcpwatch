#!/usr/bin/env bash
# End-to-end smoke test for mcpwatch: builds the CLI and the demo MCP
# server, dumps and diffs real capability surfaces, then drives a live
# watch session through an actual file edit and asserts on the printed
# diff. No network, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

# wait_for <pattern> <file>: poll (bounded) until the pattern shows up.
wait_for() {
  for _ in $(seq 1 100); do
    if grep -q "$1" "$2" 2>/dev/null; then
      return 0
    fi
    sleep 0.1
  done
  echo "--- log for debugging ---" >&2
  cat "$2" >&2 || true
  fail "timed out waiting for: $1"
}

BIN="$WORKDIR/mcpwatch"
SRV="$WORKDIR/demoserver"
SPEC="$WORKDIR/watched/caps.json"

echo "1. build the CLI and the demo server"
(cd "$ROOT" && go build -o "$BIN" ./cmd/mcpwatch) || fail "go build mcpwatch failed"
(cd "$ROOT" && go build -o "$SRV" ./examples/demoserver) || fail "go build demoserver failed"

echo "2. version matches the manifest"
"$BIN" version | grep -qx "mcpwatch 0.1.0" || fail "version mismatch"

echo "3. dump prints the full capability surface"
mkdir -p "$WORKDIR/watched"
cp "$ROOT/examples/notes-server.json" "$SPEC"
OUT="$("$BIN" dump -- "$SRV" "$SPEC")"
echo "$OUT" | grep -q "demo-notes 1.0.0 — protocol 2025-03-26" || fail "dump header missing"
echo "$OUT" | grep -q "tools (2)" || fail "dump tool count wrong"
echo "$OUT" | grep -q "summarize(date, style?)" || fail "prompt signature missing"

echo "4. dump --format json is stable and diffable"
"$BIN" dump --format json -- "$SRV" "$SPEC" > "$WORKDIR/before.json"
grep -q '"schema_version": 1' "$WORKDIR/before.json" || fail "json schema_version missing"
"$BIN" diff "$WORKDIR/before.json" "$WORKDIR/before.json" \
  | grep -q "no capability changes" || fail "self-diff should be empty"

echo "5. diff detects a changed surface and exits 1"
sed 's/"version": "1.0.0"/"version": "1.1.0"/; s/Echo a message back verbatim/Echo a message/' \
  "$ROOT/examples/notes-server.json" > "$SPEC"
"$BIN" dump --format json -- "$SRV" "$SPEC" > "$WORKDIR/after.json"
set +e
DIFF="$("$BIN" diff "$WORKDIR/before.json" "$WORKDIR/after.json")"
CODE=$?
set -e
[ "$CODE" -eq 1 ] || fail "diff with changes should exit 1, got $CODE"
echo "$DIFF" | grep -q "server: demo-notes 1.0.0 → demo-notes 1.1.0" || fail "server change missing"
echo "$DIFF" | grep -q "~ echo" || fail "changed tool missing"

echo "6. live watch session: edit → restart → printed diff"
cp "$ROOT/examples/notes-server.json" "$SPEC"
LOG="$WORKDIR/run.log"
"$BIN" run --watch "$WORKDIR/watched" --poll 100ms --debounce 100ms \
  -- "$SRV" "$SPEC" > "$LOG" 2>&1 &
RUN_PID=$!
wait_for "capability surface (2 tools, 2 resources, 1 prompt)" "$LOG"
# Edit the server's surface while the session is live.
sed 's/"tools": \[/"tools": [{"name": "slugify", "description": "Turn a title into a URL slug"},/' \
  "$ROOT/examples/notes-server.json" > "$SPEC"
wait_for "restart #1" "$LOG"
wait_for "+ slugify" "$LOG"
kill -INT "$RUN_PID"
wait "$RUN_PID" || fail "run should exit 0 on SIGINT"
grep -q "tools 2 → 3" "$LOG" || fail "tool count transition missing in diff"

echo "7. a crashing server is a runtime error (exit 3) with its stderr shown"
printf '{"name":"broken","failStartup":"boom: config exploded"}' > "$WORKDIR/broken.json"
set +e
ERR="$("$BIN" dump -- "$SRV" "$WORKDIR/broken.json" 2>&1 >/dev/null)"
CODE=$?
set -e
[ "$CODE" -eq 3 ] || fail "crashing server should exit 3, got $CODE"
echo "$ERR" | grep -q "\[server\] boom: config exploded" || fail "server stderr not forwarded"
echo "$ERR" | grep -q "exit code 1" || fail "server exit state not reported"

echo "8. usage errors exit 2"
set +e
"$BIN" dump >/dev/null 2>&1
[ $? -eq 2 ] || fail "dump without a server command should exit 2"
"$BIN" diff only-one.json >/dev/null 2>&1
[ $? -eq 2 ] || fail "diff with one file should exit 2"
set -e

echo "SMOKE OK"

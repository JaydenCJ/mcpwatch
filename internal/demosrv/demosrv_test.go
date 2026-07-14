// Tests for the demo/fake MCP server, driven over in-memory pipes with
// raw JSON-RPC lines — protocol-level assertions with no client code in
// between, so client and server bugs cannot cancel each other out.
package demosrv

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/mcpwatch/internal/jsonrpc"
)

// harness runs Serve over pipes and lets tests send raw lines and read
// decoded responses.
type harness struct {
	t   *testing.T
	in  *io.PipeWriter
	out *bufio.Scanner
}

func startHarness(t *testing.T, spec *Spec) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() {
		_ = Serve(spec, inR, outW, io.Discard)
		outW.Close()
	}()
	t.Cleanup(func() { inW.Close() })
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 64<<10), jsonrpc.MaxLineBytes)
	return &harness{t: t, in: inW, out: sc}
}

func (h *harness) send(line string) {
	h.t.Helper()
	if _, err := h.in.Write([]byte(line + "\n")); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) recv() map[string]any {
	h.t.Helper()
	if !h.out.Scan() {
		h.t.Fatal("server closed stdout unexpectedly")
	}
	var m map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &m); err != nil {
		h.t.Fatalf("bad frame %q: %v", h.out.Text(), err)
	}
	return m
}

func basicSpec() *Spec {
	return &Spec{
		Name:    "fake",
		Version: "0.9.0",
		Tools: []ToolSpec{
			{Name: "echo", Description: "Echo"},
			{Name: "add", Description: "Add", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "mul", Description: "Multiply"},
		},
		Prompts: []PromptSpec{{Name: "p1"}},
	}
}

func TestInitializeDeclaresCapabilitiesAndServerInfo(t *testing.T) {
	h := startHarness(t, basicSpec())
	h.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	res := h.recv()["result"].(map[string]any)
	if res["protocolVersion"] != DefaultProtocol {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
	caps := res["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatal("tools capability not declared")
	}
	info := res["serverInfo"].(map[string]any)
	if info["name"] != "fake" || info["version"] != "0.9.0" {
		t.Fatalf("serverInfo = %v", info)
	}
}

func TestToolsListReturnsAllToolsWithDefaultSchema(t *testing.T) {
	h := startHarness(t, basicSpec())
	h.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	res := h.recv()["result"].(map[string]any)
	tools := res["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("want 3 tools, got %d", len(tools))
	}
	first := tools[0].(map[string]any)
	if first["inputSchema"] == nil {
		t.Fatal("schema-less tool must get the default object schema")
	}
}

func TestPaginationWalksAllPages(t *testing.T) {
	spec := basicSpec()
	spec.PageSize = 2
	h := startHarness(t, spec)
	h.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	page1 := h.recv()["result"].(map[string]any)
	if n := len(page1["tools"].([]any)); n != 2 {
		t.Fatalf("page 1 size = %d", n)
	}
	cursor := page1["nextCursor"].(string)
	if cursor == "" {
		t.Fatal("page 1 must carry a nextCursor")
	}
	h.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"cursor":"` + cursor + `"}}`)
	page2 := h.recv()["result"].(map[string]any)
	if n := len(page2["tools"].([]any)); n != 1 {
		t.Fatalf("page 2 size = %d", n)
	}
	if page2["nextCursor"] != "" {
		t.Fatalf("last page must not continue, got %v", page2["nextCursor"])
	}
	// A nonsense cursor is rejected instead of restarting the list.
	h.send(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"cursor":"banana"}}`)
	if h.recv()["error"] == nil {
		t.Fatal("nonsense cursor must be rejected")
	}
}

func TestUndeclaredSectionsAndUnknownMethodsAnswerMethodNotFound(t *testing.T) {
	spec := basicSpec()
	spec.NoResources = true
	h := startHarness(t, spec)
	h.send(`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}`)
	errObj := h.recv()["error"].(map[string]any)
	if int(errObj["code"].(float64)) != jsonrpc.CodeMethodNotFound {
		t.Fatalf("undeclared section: want -32601, got %v", errObj)
	}
	h.send(`{"jsonrpc":"2.0","id":2,"method":"sampling/createMessage","params":{}}`)
	errObj = h.recv()["error"].(map[string]any)
	if int(errObj["code"].(float64)) != jsonrpc.CodeMethodNotFound {
		t.Fatalf("unknown method: want -32601, got %v", errObj)
	}
}

func TestToolsCallEchoIsAWorkingTool(t *testing.T) {
	h := startHarness(t, basicSpec())
	h.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi there"}}}`)
	res := h.recv()["result"].(map[string]any)
	content := res["content"].([]any)[0].(map[string]any)
	if content["text"] != "hi there" {
		t.Fatalf("echo answered %v", content)
	}
}

func TestNotificationsProduceNoResponse(t *testing.T) {
	h := startHarness(t, basicSpec())
	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	// If the notification wrongly produced a frame, ping's response
	// would arrive out of order below.
	h.send(`{"jsonrpc":"2.0","id":9,"method":"ping"}`)
	if got := h.recv()["id"].(float64); got != 9 {
		t.Fatalf("expected ping response first, got id %v", got)
	}
}

func TestServeReturnsCleanlyOnEOF(t *testing.T) {
	spec := basicSpec()
	done := make(chan error, 1)
	inR, inW := io.Pipe()
	go func() { done <- Serve(spec, inR, io.Discard, io.Discard) }()
	inW.Close()
	if err := <-done; err != nil {
		t.Fatalf("EOF is a normal shutdown, got %v", err)
	}
}

func TestFailStartupWritesStderrAndErrors(t *testing.T) {
	var stderr strings.Builder
	err := Serve(&Spec{Name: "b", FailStartup: "config exploded"}, strings.NewReader(""), io.Discard, &stderr)
	if err == nil || !strings.Contains(stderr.String(), "config exploded") {
		t.Fatalf("err=%v stderr=%q", err, stderr.String())
	}
}

func TestLoadSpecValidates(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"name":"x","version":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSpec(good); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	unnamed := filepath.Join(dir, "unnamed.json")
	if err := os.WriteFile(unnamed, []byte(`{"version":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSpec(unnamed); err == nil {
		t.Fatal("spec without name must be rejected")
	}
	if _, err := LoadSpec(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing file must be an error")
	}
}

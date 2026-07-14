// Tests for the MCP client. Happy paths run against the real demosrv
// implementation over in-memory pipes; edge cases (hangs, stale
// responses, server-initiated requests) use hand-scripted pipes so the
// exact wire sequence is visible in the test.
package mcpclient

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/mcpwatch/internal/demosrv"
	"github.com/JaydenCJ/mcpwatch/internal/jsonrpc"
)

// guardTimeout is a generous upper bound that only trips when a test
// has genuinely deadlocked; it never gates the happy path.
const guardTimeout = 10 * time.Second

func startDemo(t *testing.T, spec *demosrv.Spec) *Client {
	t.Helper()
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	go func() {
		_ = demosrv.Serve(spec, serverRead, serverWrite, io.Discard)
		serverWrite.Close()
	}()
	t.Cleanup(func() { clientWrite.Close() })
	c := New(clientRead, clientWrite)
	c.Timeout = guardTimeout
	return c
}

func notesSpec() *demosrv.Spec {
	return &demosrv.Spec{
		Name:    "notes",
		Version: "1.0.0",
		Tools: []demosrv.ToolSpec{
			{Name: "zeta", Description: "Z tool"},
			{Name: "alpha", Description: "A tool", InputSchema: json.RawMessage(`{"type":"object","required":["x"]}`)},
		},
		Resources: []demosrv.ResourceSpec{{URI: "notes://today", Name: "Today"}},
		Templates: []demosrv.TemplateSpec{{URITemplate: "notes://{date}"}},
		Prompts:   []demosrv.PromptSpec{{Name: "sum", Arguments: []demosrv.PromptArgSpec{{Name: "date", Required: true}}}},
	}
}

func TestInitializeHandshake(t *testing.T) {
	c := startDemo(t, notesSpec())
	res, err := c.Initialize("")
	if err != nil {
		t.Fatal(err)
	}
	if res.ServerInfo.Name != "notes" || res.ProtocolVersion != demosrv.DefaultProtocol {
		t.Fatalf("init result = %+v", res)
	}
	if _, ok := res.Capabilities["tools"]; !ok {
		t.Fatal("capabilities not surfaced")
	}
}

func TestDumpSnapshotCollectsEverySection(t *testing.T) {
	c := startDemo(t, notesSpec())
	snap, err := c.DumpSnapshot("")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tools) != 2 || len(snap.Resources) != 1 || len(snap.ResourceTemplates) != 1 || len(snap.Prompts) != 1 {
		t.Fatalf("snapshot incomplete: %s", snap.Counts())
	}
	if !snap.ToolsSupported || !snap.ResourcesSupported || !snap.PromptsSupported {
		t.Fatal("support flags should all be true")
	}
	// The snapshot must already be normalized: sorted and hashed.
	if snap.Tools[0].Name != "alpha" {
		t.Fatalf("tools must come back sorted, got %s first", snap.Tools[0].Name)
	}
	if snap.Tools[0].SchemaHash == "" {
		t.Fatal("schema hash missing after dump")
	}
}

func TestDumpSnapshotFollowsPagination(t *testing.T) {
	spec := notesSpec()
	spec.PageSize = 1 // force a cursor round trip per item
	c := startDemo(t, spec)
	snap, err := c.DumpSnapshot("")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tools) != 2 {
		t.Fatalf("pagination lost tools: %d", len(snap.Tools))
	}
}

func TestUndeclaredSectionsAreSkipped(t *testing.T) {
	spec := notesSpec()
	spec.NoPrompts = true
	spec.NoResources = true
	c := startDemo(t, spec)
	snap, err := c.DumpSnapshot("")
	if err != nil {
		t.Fatal(err)
	}
	if snap.PromptsSupported || snap.ResourcesSupported {
		t.Fatal("undeclared sections must report unsupported")
	}
	if !snap.ToolsSupported {
		t.Fatal("tools are declared and must stay supported")
	}
}

// scripted starts a client against a hand-written server function that
// gets the raw request lines and a writer for its answers.
func scripted(t *testing.T, server func(in *bufio.Scanner, out io.Writer)) *Client {
	t.Helper()
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	go func() {
		sc := bufio.NewScanner(serverRead)
		sc.Buffer(make([]byte, 64<<10), jsonrpc.MaxLineBytes)
		server(sc, serverWrite)
		serverWrite.Close()
	}()
	t.Cleanup(func() { clientWrite.Close() })
	c := New(clientRead, clientWrite)
	c.Timeout = guardTimeout
	return c
}

func TestCallTimesOutOnASilentServer(t *testing.T) {
	c := scripted(t, func(in *bufio.Scanner, out io.Writer) {
		in.Scan() // swallow the request, answer nothing
		in.Scan() // hold the pipe open until the client gives up
	})
	c.Timeout = 20 * time.Millisecond // always trips: the server is silent by construction
	_, err := c.Call("initialize", nil)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestCloseUnblocksTheReaderAfterAnAbandonedCall(t *testing.T) {
	// A server that answers only after the client has already timed out
	// and moved on: without Close, the reader goroutine would block
	// forever trying to deliver that answer to nobody.
	callGaveUp := make(chan struct{})
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	go func() {
		sc := bufio.NewScanner(serverRead)
		sc.Scan()    // the request the client will give up on
		<-callGaveUp // deterministically answer too late
		io.WriteString(serverWrite, `{"jsonrpc":"2.0","id":1,"result":{"late":true}}`+"\n")
		sc.Scan() // hold the pipe open; only Close can free the reader now
	}()
	t.Cleanup(func() { clientWrite.Close(); serverWrite.Close() })
	c := New(clientRead, clientWrite)
	c.Timeout = 20 * time.Millisecond
	if _, err := c.Call("initialize", nil); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	close(callGaveUp) // the late answer now wedges the reader on delivery
	c.Close()
	c.Close() // must be idempotent
	select {
	case <-c.lines: // reader exited: it closed its channel on the way out
	case <-time.After(guardTimeout):
		t.Fatal("reader goroutine still blocked after Close")
	}
}

func TestCallSkipsStaleResponsesAndNotifications(t *testing.T) {
	c := scripted(t, func(in *bufio.Scanner, out io.Writer) {
		in.Scan()
		// A stale response (wrong id), then a notification, then the answer.
		io.WriteString(out, `{"jsonrpc":"2.0","id":999,"result":{"stale":true}}`+"\n")
		io.WriteString(out, `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`+"\n")
		io.WriteString(out, `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`+"\n")
	})
	var notified []string
	c.OnNotification = func(m string) { notified = append(notified, m) }
	res, err := c.Call("ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res), `"ok":true`) {
		t.Fatalf("got the wrong response: %s", res)
	}
	if len(notified) != 1 || notified[0] != "notifications/tools/list_changed" {
		t.Fatalf("notification hook missed: %v", notified)
	}
}

func TestServerInitiatedRequestIsDeclined(t *testing.T) {
	got := make(chan string, 1)
	c := scripted(t, func(in *bufio.Scanner, out io.Writer) {
		in.Scan()
		// The server asks us something mid-call (e.g. sampling).
		io.WriteString(out, `{"jsonrpc":"2.0","id":"srv-1","method":"sampling/createMessage","params":{}}`+"\n")
		in.Scan() // the client's refusal
		got <- in.Text()
		io.WriteString(out, `{"jsonrpc":"2.0","id":1,"result":{}}`+"\n")
	})
	if _, err := c.Call("ping", nil); err != nil {
		t.Fatal(err)
	}
	refusal := <-got
	if !strings.Contains(refusal, `"id":"srv-1"`) || !strings.Contains(refusal, "-32601") {
		t.Fatalf("server request must be answered with method-not-found on its own id: %s", refusal)
	}
}

func TestErrorResponseSurfacesAsJSONRPCError(t *testing.T) {
	c := scripted(t, func(in *bufio.Scanner, out io.Writer) {
		in.Scan()
		io.WriteString(out, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"kaput"}}`+"\n")
	})
	_, err := c.Call("initialize", nil)
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32000 {
		t.Fatalf("want wrapped *jsonrpc.Error, got %v", err)
	}
}

func TestBrokenStreamsFailTheCallInsteadOfHanging(t *testing.T) {
	// Server exits without answering: the harness closes the write side.
	c := scripted(t, func(in *bufio.Scanner, out io.Writer) {
		in.Scan()
	})
	_, err := c.Call("initialize", nil)
	if err == nil || !strings.Contains(err.Error(), "closed its stdout") {
		t.Fatalf("want a closed-stdout error, got %v", err)
	}

	// Server prints a crash trace to stdout instead of JSON-RPC.
	c = scripted(t, func(in *bufio.Scanner, out io.Writer) {
		in.Scan()
		io.WriteString(out, "Traceback (most recent call last):\n")
	})
	if _, err := c.Call("initialize", nil); err == nil {
		t.Fatal("non-JSON stdout must fail the call, not hang it")
	}
}

func TestRunawayPaginationIsBounded(t *testing.T) {
	c := scripted(t, func(in *bufio.Scanner, out io.Writer) {
		// initialize
		in.Scan()
		io.WriteString(out, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"loop","version":"0"}}}`+"\n")
		in.Scan() // notifications/initialized
		// tools/list forever: always the same cursor.
		id := 2
		for in.Scan() {
			line := `{"jsonrpc":"2.0","id":` + jsonInt(id) + `,"result":{"tools":[],"nextCursor":"again"}}` + "\n"
			io.WriteString(out, line)
			id++
		}
	})
	_, err := c.DumpSnapshot("")
	if err == nil || !strings.Contains(err.Error(), "pagination did not terminate") {
		t.Fatalf("runaway cursor must be detected, got %v", err)
	}
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

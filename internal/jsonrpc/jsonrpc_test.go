// Tests for the JSON-RPC wire layer: encoding shape, decoding
// validation, message classification, and the line reader's tolerance
// for real-world stream noise.
package jsonrpc

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestMarshalRequestAndNotificationShapes(t *testing.T) {
	line, err := MarshalRequest(7, "tools/list", map[string]string{"cursor": "3"})
	if err != nil {
		t.Fatal(err)
	}
	if line[len(line)-1] != '\n' {
		t.Fatal("frames must be newline-terminated")
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatal(err)
	}
	if m["jsonrpc"] != "2.0" || m["method"] != "tools/list" || m["id"] != float64(7) {
		t.Fatalf("bad request envelope: %v", m)
	}

	line, err = MarshalNotification("notifications/initialized", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(line), `"id"`) {
		t.Fatalf("notification must not carry an id: %s", line)
	}
}

func TestMarshalResponsesEchoRawID(t *testing.T) {
	// String ids must survive verbatim — mcpwatch answers server
	// requests using whatever id type the server chose.
	line, err := MarshalResult(json.RawMessage(`"abc"`), map[string]int{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), `"id":"abc"`) {
		t.Fatalf("string id mangled: %s", line)
	}

	line, err = MarshalError(json.RawMessage(`5`), CodeMethodNotFound, "nope")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := Decode(line)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Error == nil || msg.Error.Code != CodeMethodNotFound || msg.Error.Message != "nope" {
		t.Fatalf("error envelope wrong: %+v", msg.Error)
	}
}

func TestDecodeRejectsNonProtocolLines(t *testing.T) {
	// Stray log lines on stdout must be rejected, not swallowed.
	if _, err := Decode([]byte("Server listening on port 8080")); err == nil {
		t.Fatal("non-JSON line accepted")
	}
	if _, err := Decode([]byte(`{"jsonrpc":"1.0","id":1,"result":{}}`)); err == nil {
		t.Fatal("jsonrpc 1.0 frames must be rejected")
	}
}

func TestKindClassification(t *testing.T) {
	cases := []struct {
		line string
		want Kind
	}{
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, KindRequest},
		{`{"jsonrpc":"2.0","method":"notifications/initialized"}`, KindNotification},
		{`{"jsonrpc":"2.0","id":1,"result":{}}`, KindResponse},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"x"}}`, KindResponse},
		{`{"jsonrpc":"2.0","id":null,"method":"ping"}`, KindNotification}, // null id ≙ no id
		{`{"jsonrpc":"2.0"}`, KindInvalid},
	}
	for _, c := range cases {
		msg, err := Decode([]byte(c.line))
		if err != nil {
			t.Fatalf("%s: %v", c.line, err)
		}
		if got := msg.Kind(); got != c.want {
			t.Errorf("Kind(%s) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestErrorImplementsGoError(t *testing.T) {
	e := &Error{Code: -32601, Message: "method not found"}
	if !strings.Contains(e.Error(), "-32601") {
		t.Fatalf("Error() should include the code: %q", e.Error())
	}
}

func TestLineReaderSkipsBlankLinesAndEndsWithEOF(t *testing.T) {
	in := "\n\n" + `{"jsonrpc":"2.0","id":1,"result":{}}` + "\n\n" + `{"jsonrpc":"2.0","method":"n"}` + "\n"
	lr := NewLineReader(strings.NewReader(in))
	first, err := lr.Read()
	if err != nil || first.Kind() != KindResponse {
		t.Fatalf("first read: %v %v", first, err)
	}
	second, err := lr.Read()
	if err != nil || second.Method != "n" {
		t.Fatalf("second read: %v %v", second, err)
	}
	if _, err := lr.Read(); err != io.EOF {
		t.Fatalf("want io.EOF at end, got %v", err)
	}
	// And a garbage line surfaces an error instead of hanging.
	if _, err := NewLineReader(strings.NewReader("garbage\n")).Read(); err == nil {
		t.Fatal("garbage line must surface an error")
	}
}

func TestLineReaderHandlesLargeFrames(t *testing.T) {
	// A 1 MiB tool schema must fit in one frame; bufio's default 64 KiB
	// limit would split it, so this guards the buffer sizing.
	big := strings.Repeat("x", 1<<20)
	in := `{"jsonrpc":"2.0","id":1,"result":{"blob":"` + big + `"}}` + "\n"
	lr := NewLineReader(strings.NewReader(in))
	msg, err := lr.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Result) < 1<<20 {
		t.Fatal("large frame truncated")
	}
}

// Package jsonrpc implements the wire layer mcpwatch shares with every
// stdio MCP server: JSON-RPC 2.0 messages, one per line, UTF-8. It only
// covers what a capability-dumping client needs — encoding requests and
// notifications, decoding whatever the server sends back, and
// classifying each inbound message.
package jsonrpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Version is the only JSON-RPC version MCP speaks.
const Version = "2.0"

// Standard JSON-RPC error codes mcpwatch cares about.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
)

// MaxLineBytes bounds a single message. 16 MiB comfortably fits large
// tool schemas while still refusing a runaway server that never emits
// a newline.
const MaxLineBytes = 16 << 20

// Error is a JSON-RPC error object. It doubles as a Go error so callers
// can surface server-reported failures directly.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("server error %d: %s", e.Code, e.Message)
}

// Kind classifies an inbound message.
type Kind int

const (
	KindInvalid Kind = iota
	KindRequest
	KindNotification
	KindResponse
)

// Message is a decoded JSON-RPC message. ID keeps its raw encoding so
// numeric and string ids survive a round trip untouched.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Kind reports what role the message plays.
func (m *Message) Kind() Kind {
	hasID := len(m.ID) > 0 && !bytes.Equal(m.ID, []byte("null"))
	switch {
	case m.Method != "" && hasID:
		return KindRequest
	case m.Method != "":
		return KindNotification
	case hasID && (m.Result != nil || m.Error != nil):
		return KindResponse
	default:
		return KindInvalid
	}
}

// Decode parses one line into a Message, rejecting anything that is not
// JSON-RPC 2.0.
func Decode(line []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, fmt.Errorf("invalid JSON-RPC frame: %w", err)
	}
	if m.JSONRPC != Version {
		return nil, fmt.Errorf("invalid JSON-RPC frame: jsonrpc field is %q, want %q", m.JSONRPC, Version)
	}
	return &m, nil
}

// envelope is the encoding-side counterpart of Message; a separate type
// keeps omitempty behavior explicit per constructor.
type envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func marshalLine(env envelope) ([]byte, error) {
	b, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// MarshalRequest encodes a request with a numeric id, newline-terminated.
func MarshalRequest(id int64, method string, params any) ([]byte, error) {
	raw, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	return marshalLine(envelope{JSONRPC: Version, ID: raw, Method: method, Params: params})
}

// MarshalNotification encodes a notification, newline-terminated.
func MarshalNotification(method string, params any) ([]byte, error) {
	return marshalLine(envelope{JSONRPC: Version, Method: method, Params: params})
}

// MarshalResult encodes a success response for the given raw id.
func MarshalResult(id json.RawMessage, result any) ([]byte, error) {
	if result == nil {
		result = struct{}{}
	}
	return marshalLine(envelope{JSONRPC: Version, ID: id, Result: result})
}

// MarshalError encodes an error response for the given raw id.
func MarshalError(id json.RawMessage, code int, message string) ([]byte, error) {
	return marshalLine(envelope{JSONRPC: Version, ID: id, Error: &Error{Code: code, Message: message}})
}

// LineReader yields decoded messages from a newline-delimited stream,
// skipping blank lines (some servers emit a trailing one on shutdown).
type LineReader struct {
	sc *bufio.Scanner
}

// NewLineReader wraps r with a scanner sized for MaxLineBytes frames.
func NewLineReader(r io.Reader) *LineReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), MaxLineBytes)
	return &LineReader{sc: sc}
}

// Read returns the next message, or io.EOF once the stream ends.
func (lr *LineReader) Read() (*Message, error) {
	for lr.sc.Scan() {
		line := bytes.TrimSpace(lr.sc.Bytes())
		if len(line) == 0 {
			continue
		}
		return Decode(line)
	}
	if err := lr.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

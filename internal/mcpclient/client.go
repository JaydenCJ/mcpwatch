// Package mcpclient is the minimal MCP client mcpwatch needs: perform
// the initialize handshake over stdio, then page through tools/list,
// resources/list, resources/templates/list, and prompts/list to build a
// capability.Snapshot. It tolerates the quirks that show up in servers
// under active development — sections that are declared but answer
// "method not found", notifications interleaved with responses, and
// servers that simply hang (bounded by a per-call timeout).
package mcpclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/JaydenCJ/mcpwatch/internal/capability"
	"github.com/JaydenCJ/mcpwatch/internal/jsonrpc"
	"github.com/JaydenCJ/mcpwatch/internal/version"
)

// DefaultProtocolVersion is the MCP revision mcpwatch requests during
// initialize; servers are free to answer with the version they speak.
const DefaultProtocolVersion = "2025-03-26"

// maxPages bounds list pagination so a server that returns the same
// cursor forever cannot wedge the dev loop.
const maxPages = 100

// ErrTimeout is returned when the server does not answer a call within
// the client's Timeout.
var ErrTimeout = errors.New("timed out waiting for server response")

type inbound struct {
	msg *jsonrpc.Message
	err error
}

// Client drives one server process over its stdio pipes. It is not
// safe for concurrent calls — mcpwatch's dev loop is sequential by
// design, one request in flight at a time.
type Client struct {
	// Timeout bounds each call (and the initialize handshake). Zero
	// means wait forever.
	Timeout time.Duration
	// OnNotification, when set, observes server notifications (e.g.
	// notifications/tools/list_changed) that arrive between responses.
	OnNotification func(method string)

	w         io.Writer
	wmu       sync.Mutex
	lines     chan inbound
	nextID    int64
	closed    chan struct{}
	closeOnce sync.Once
}

// New wraps the server's stdout (r) and stdin (w) and starts the reader
// goroutine. The goroutine exits when the server closes its stdout, or
// when Close is called.
func New(r io.Reader, w io.Writer) *Client {
	c := &Client{w: w, lines: make(chan inbound), closed: make(chan struct{})}
	lr := jsonrpc.NewLineReader(r)
	go func() {
		defer close(c.lines)
		for {
			msg, err := lr.Read()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					c.deliver(inbound{err: err})
				}
				return
			}
			if !c.deliver(inbound{msg: msg}) {
				return
			}
		}
	}()
	return c
}

// deliver hands one inbound item to a waiting Call, bailing out once the
// client is closed so the reader goroutine can never block forever on a
// send nobody will receive (e.g. after a timeout abandoned the call).
func (c *Client) deliver(in inbound) bool {
	select {
	case c.lines <- in:
		return true
	case <-c.closed:
		return false
	}
}

// Close releases the reader goroutine. It does not close the underlying
// pipes — the process owner does that. Safe to call more than once.
func (c *Client) Close() {
	c.closeOnce.Do(func() { close(c.closed) })
}

func (c *Client) write(line []byte, err error) error {
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, werr := c.w.Write(line)
	return werr
}

// Notify sends a notification (no response expected).
func (c *Client) Notify(method string, params any) error {
	line, err := jsonrpc.MarshalNotification(method, params)
	return c.write(line, err)
}

// Call sends a request and waits for its response, dispatching any
// interleaved notifications and politely refusing any server-initiated
// request (mcpwatch is a dumper, not a sampling host).
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	line, err := jsonrpc.MarshalRequest(id, method, params)
	if err := c.write(line, err); err != nil {
		return nil, fmt.Errorf("%s: write: %w", method, err)
	}

	var deadline <-chan time.Time
	if c.Timeout > 0 {
		timer := time.NewTimer(c.Timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	for {
		select {
		case in, ok := <-c.lines:
			if !ok {
				return nil, fmt.Errorf("%s: server closed its stdout before responding", method)
			}
			if in.err != nil {
				return nil, fmt.Errorf("%s: %w", method, in.err)
			}
			done, res, err := c.dispatch(id, in.msg)
			if done {
				if err != nil {
					return nil, fmt.Errorf("%s: %w", method, err)
				}
				return res, nil
			}
		case <-deadline:
			return nil, fmt.Errorf("%s: %w after %s", method, ErrTimeout, c.Timeout)
		}
	}
}

// dispatch handles one inbound message while a call is pending. It
// returns done=true with the result or error once the matching
// response arrives.
func (c *Client) dispatch(wantID int64, msg *jsonrpc.Message) (done bool, res json.RawMessage, err error) {
	switch msg.Kind() {
	case jsonrpc.KindResponse:
		var got int64
		if jerr := json.Unmarshal(msg.ID, &got); jerr != nil || got != wantID {
			return false, nil, nil // a stale or foreign response; keep waiting
		}
		if msg.Error != nil {
			return true, nil, msg.Error
		}
		return true, msg.Result, nil
	case jsonrpc.KindNotification:
		if c.OnNotification != nil {
			c.OnNotification(msg.Method)
		}
		return false, nil, nil
	case jsonrpc.KindRequest:
		// e.g. sampling/createMessage — decline without breaking the wire.
		reply, merr := jsonrpc.MarshalError(msg.ID, jsonrpc.CodeMethodNotFound,
			"mcpwatch does not implement "+strconv.Quote(msg.Method))
		_ = c.write(reply, merr)
		return false, nil, nil
	default:
		return false, nil, nil // ignore malformed noise; the timeout backstops us
	}
}

// InitResult is the part of the initialize response mcpwatch uses.
type InitResult struct {
	ProtocolVersion string                     `json:"protocolVersion"`
	ServerInfo      capability.ServerInfo      `json:"serverInfo"`
	Capabilities    map[string]json.RawMessage `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    struct{}   `json:"capabilities"`
	ClientInfo      clientInfo `json:"clientInfo"`
}

// Initialize performs the MCP handshake: initialize request, then the
// notifications/initialized notification.
func (c *Client) Initialize(protocol string) (*InitResult, error) {
	if protocol == "" {
		protocol = DefaultProtocolVersion
	}
	params := initParams{ProtocolVersion: protocol}
	params.ClientInfo = clientInfo{Name: "mcpwatch", Version: version.Version}
	raw, err := c.Call("initialize", params)
	if err != nil {
		return nil, err
	}
	var res InitResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("initialize: malformed result: %w", err)
	}
	if err := c.Notify("notifications/initialized", nil); err != nil {
		return nil, fmt.Errorf("notifications/initialized: %w", err)
	}
	return &res, nil
}

// listPage is the shared shape of every paginated list result.
type listPage struct {
	Tools             []capability.Tool             `json:"tools"`
	Resources         []capability.Resource         `json:"resources"`
	ResourceTemplates []capability.ResourceTemplate `json:"resourceTemplates"`
	Prompts           []capability.Prompt           `json:"prompts"`
	NextCursor        string                        `json:"nextCursor"`
}

type cursorParams struct {
	Cursor string `json:"cursor,omitempty"`
}

// listAll pages through one list method. A "method not found" answer is
// reported as supported=false rather than an error: servers in mid-
// refactor lie about their capabilities all the time, and the dev loop
// must keep going.
func (c *Client) listAll(method string, collect func(listPage)) (supported bool, err error) {
	cursor := ""
	for page := 0; page < maxPages; page++ {
		raw, err := c.Call(method, cursorParams{Cursor: cursor})
		if err != nil {
			var rpcErr *jsonrpc.Error
			if errors.As(err, &rpcErr) && rpcErr.Code == jsonrpc.CodeMethodNotFound {
				return false, nil
			}
			return false, err
		}
		var pg listPage
		if err := json.Unmarshal(raw, &pg); err != nil {
			return false, fmt.Errorf("%s: malformed result: %w", method, err)
		}
		collect(pg)
		if pg.NextCursor == "" {
			return true, nil
		}
		cursor = pg.NextCursor
	}
	return false, fmt.Errorf("%s: pagination did not terminate after %d pages", method, maxPages)
}

// DumpSnapshot runs the full handshake + listing sequence and returns a
// normalized capability snapshot. Sections the server does not declare
// in its initialize capabilities are skipped without a wire call.
func (c *Client) DumpSnapshot(protocol string) (*capability.Snapshot, error) {
	init, err := c.Initialize(protocol)
	if err != nil {
		return nil, err
	}
	snap := &capability.Snapshot{
		ProtocolVersion: init.ProtocolVersion,
		ServerInfo:      init.ServerInfo,
	}
	_, declaresTools := init.Capabilities["tools"]
	_, declaresResources := init.Capabilities["resources"]
	_, declaresPrompts := init.Capabilities["prompts"]

	if declaresTools {
		snap.ToolsSupported, err = c.listAll("tools/list", func(pg listPage) {
			snap.Tools = append(snap.Tools, pg.Tools...)
		})
		if err != nil {
			return nil, err
		}
	}
	if declaresResources {
		snap.ResourcesSupported, err = c.listAll("resources/list", func(pg listPage) {
			snap.Resources = append(snap.Resources, pg.Resources...)
		})
		if err != nil {
			return nil, err
		}
		// Templates are optional even for servers with resources.
		if _, err = c.listAll("resources/templates/list", func(pg listPage) {
			snap.ResourceTemplates = append(snap.ResourceTemplates, pg.ResourceTemplates...)
		}); err != nil {
			return nil, err
		}
	}
	if declaresPrompts {
		snap.PromptsSupported, err = c.listAll("prompts/list", func(pg listPage) {
			snap.Prompts = append(snap.Prompts, pg.Prompts...)
		})
		if err != nil {
			return nil, err
		}
	}
	if err := snap.Normalize(); err != nil {
		return nil, err
	}
	return snap, nil
}

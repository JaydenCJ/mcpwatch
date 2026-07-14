// Package demosrv is a tiny, spec-driven MCP stdio server. It exists
// for two reasons: it powers examples/demoserver, the runnable demo the
// README and smoke test iterate on, and it doubles as the fake server
// the test suite talks to over in-process pipes — so the exact code
// paths users see in the demo are the ones under test.
//
// The server's whole surface is described by a JSON Spec file; edit the
// file, restart the process, and the surface changes. That is precisely
// the loop mcpwatch is built to observe.
package demosrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/JaydenCJ/mcpwatch/internal/jsonrpc"
)

// DefaultProtocol is the MCP revision the demo server claims to speak.
const DefaultProtocol = "2025-03-26"

// ToolSpec declares one tool. A nil InputSchema defaults to the empty
// object schema, matching what minimal real servers emit.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ResourceSpec declares one concrete resource.
type ResourceSpec struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// TemplateSpec declares one resource template.
type TemplateSpec struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// PromptArgSpec declares one prompt argument.
type PromptArgSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptSpec declares one prompt.
type PromptSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Arguments   []PromptArgSpec `json:"arguments,omitempty"`
}

// Spec is the whole server, as JSON. The No* flags simulate servers
// that do not declare a capability section at all (list calls then
// answer "method not found").
type Spec struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Protocol    string         `json:"protocol,omitempty"`
	Tools       []ToolSpec     `json:"tools,omitempty"`
	NoTools     bool           `json:"noTools,omitempty"`
	Resources   []ResourceSpec `json:"resources,omitempty"`
	Templates   []TemplateSpec `json:"resourceTemplates,omitempty"`
	NoResources bool           `json:"noResources,omitempty"`
	Prompts     []PromptSpec   `json:"prompts,omitempty"`
	NoPrompts   bool           `json:"noPrompts,omitempty"`
	// PageSize > 0 paginates every list response, exercising cursors.
	PageSize int `json:"pageSize,omitempty"`
	// FailStartup, when non-empty, makes the server print the message
	// to stderr and exit 1 before speaking any JSON-RPC — the "I broke
	// my server and it no longer boots" scenario.
	FailStartup string `json:"failStartup,omitempty"`
}

// LoadSpec reads and validates a Spec file.
func LoadSpec(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if spec.Name == "" {
		return nil, fmt.Errorf("%s: spec is missing \"name\"", path)
	}
	return &spec, nil
}

// Serve speaks MCP on r/w until the client closes stdin. It returns an
// error only for startup failures; protocol-level problems are answered
// on the wire, as a real server would.
func Serve(spec *Spec, r io.Reader, w io.Writer, errw io.Writer) error {
	if spec.FailStartup != "" {
		fmt.Fprintln(errw, spec.FailStartup)
		return errors.New(spec.FailStartup)
	}
	s := &server{spec: spec, w: w}
	lr := jsonrpc.NewLineReader(r)
	for {
		msg, err := lr.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // client hung up; normal shutdown
			}
			return err
		}
		s.handle(msg)
	}
}

type server struct {
	spec *Spec
	w    io.Writer
}

func (s *server) send(line []byte, err error) {
	if err == nil {
		_, _ = s.w.Write(line)
	}
}

func (s *server) reply(id json.RawMessage, result any) {
	s.send(jsonrpc.MarshalResult(id, result))
}

func (s *server) replyError(id json.RawMessage, code int, msg string) {
	s.send(jsonrpc.MarshalError(id, code, msg))
}

func (s *server) handle(msg *jsonrpc.Message) {
	switch msg.Kind() {
	case jsonrpc.KindNotification:
		return // nothing to do for initialized/cancelled
	case jsonrpc.KindRequest:
	default:
		return
	}
	switch msg.Method {
	case "initialize":
		s.reply(msg.ID, s.initResult())
	case "ping":
		s.reply(msg.ID, struct{}{})
	case "tools/list":
		s.list(msg, !s.spec.NoTools, len(s.spec.Tools), func(lo, hi int, next string) any {
			return map[string]any{"tools": toolsOut(s.spec.Tools[lo:hi]), "nextCursor": next}
		})
	case "resources/list":
		s.list(msg, !s.spec.NoResources, len(s.spec.Resources), func(lo, hi int, next string) any {
			return map[string]any{"resources": s.spec.Resources[lo:hi], "nextCursor": next}
		})
	case "resources/templates/list":
		s.list(msg, !s.spec.NoResources, len(s.spec.Templates), func(lo, hi int, next string) any {
			return map[string]any{"resourceTemplates": s.spec.Templates[lo:hi], "nextCursor": next}
		})
	case "prompts/list":
		s.list(msg, !s.spec.NoPrompts, len(s.spec.Prompts), func(lo, hi int, next string) any {
			return map[string]any{"prompts": s.spec.Prompts[lo:hi], "nextCursor": next}
		})
	case "tools/call":
		s.callTool(msg)
	default:
		s.replyError(msg.ID, jsonrpc.CodeMethodNotFound, "method not found: "+msg.Method)
	}
}

func (s *server) initResult() map[string]any {
	caps := map[string]any{}
	if !s.spec.NoTools {
		caps["tools"] = map[string]any{"listChanged": false}
	}
	if !s.spec.NoResources {
		caps["resources"] = map[string]any{"listChanged": false}
	}
	if !s.spec.NoPrompts {
		caps["prompts"] = map[string]any{"listChanged": false}
	}
	proto := s.spec.Protocol
	if proto == "" {
		proto = DefaultProtocol
	}
	return map[string]any{
		"protocolVersion": proto,
		"capabilities":    caps,
		"serverInfo":      map[string]any{"name": s.spec.Name, "version": s.spec.Version},
	}
}

// list answers one paginated list call. Cursors are plain integer
// offsets — opaque enough for a demo, trivial to verify in tests.
func (s *server) list(msg *jsonrpc.Message, supported bool, total int, page func(lo, hi int, next string) any) {
	if !supported {
		s.replyError(msg.ID, jsonrpc.CodeMethodNotFound, "method not found: "+msg.Method)
		return
	}
	lo := 0
	if len(msg.Params) > 0 {
		var p struct {
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(msg.Params, &p); err == nil && p.Cursor != "" {
			n, err := strconv.Atoi(p.Cursor)
			if err != nil || n < 0 || n > total {
				s.replyError(msg.ID, jsonrpc.CodeInvalidRequest, "invalid cursor")
				return
			}
			lo = n
		}
	}
	hi := total
	next := ""
	if s.spec.PageSize > 0 && lo+s.spec.PageSize < total {
		hi = lo + s.spec.PageSize
		next = strconv.Itoa(hi)
	}
	s.reply(msg.ID, page(lo, hi, next))
}

// toolsOut fills in the default empty-object schema for tools that
// declared none, mirroring common SDK behavior.
func toolsOut(tools []ToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		})
	}
	return out
}

// callTool implements one working tool ("echo") so the demo server is a
// real server, not a mannequin.
func (s *server) callTool(msg *jsonrpc.Message) {
	var p struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.replyError(msg.ID, jsonrpc.CodeInvalidRequest, "invalid tools/call params")
		return
	}
	for _, t := range s.spec.Tools {
		if t.Name != p.Name {
			continue
		}
		text := "ok"
		if raw, ok := p.Arguments["text"]; ok {
			var v string
			if json.Unmarshal(raw, &v) == nil {
				text = v
			}
		}
		s.reply(msg.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		})
		return
	}
	s.replyError(msg.ID, jsonrpc.CodeInvalidRequest, "unknown tool: "+p.Name)
}

// Package capability defines the snapshot of an MCP server's public
// surface — tools, resources, resource templates, and prompts — plus the
// canonicalization that makes snapshots comparable: lists are sorted,
// input schemas are re-encoded with sorted keys, and every schema gets a
// short content hash so a diff can say *that* a schema changed without
// printing the whole document.
package capability

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SchemaVersion identifies the JSON layout emitted by Encode. Bump it
// whenever a field changes meaning, so downstream scripts can gate.
const SchemaVersion = 1

// ServerInfo mirrors the MCP initialize result's serverInfo object.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// String renders "name version" (or just the name when the server sends
// no version), the form used in diff banners.
func (s ServerInfo) String() string {
	if s.Version == "" {
		return s.Name
	}
	return s.Name + " " + s.Version
}

// Tool is one entry from tools/list. InputSchema is stored in canonical
// form; SchemaHash is the first 12 hex chars of its SHA-256.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	SchemaHash  string          `json:"schemaHash,omitempty"`
}

// Resource is one entry from resources/list, keyed by URI.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ResourceTemplate is one entry from resources/templates/list.
type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// PromptArgument is one declared argument of a prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Prompt is one entry from prompts/list.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// Signature renders the prompt's argument list as "a, b?" (a `?` marks
// optional arguments); diffs compare this compact form.
func (p Prompt) Signature() string {
	parts := make([]string, 0, len(p.Arguments))
	for _, a := range p.Arguments {
		if a.Required {
			parts = append(parts, a.Name)
		} else {
			parts = append(parts, a.Name+"?")
		}
	}
	return strings.Join(parts, ", ")
}

// Snapshot is the full capability surface of one server run. The
// *Supported flags distinguish "declared but currently empty" from "the
// server does not offer this section at all".
type Snapshot struct {
	Schema             int                `json:"schema_version"`
	ProtocolVersion    string             `json:"protocolVersion"`
	ServerInfo         ServerInfo         `json:"serverInfo"`
	ToolsSupported     bool               `json:"toolsSupported"`
	Tools              []Tool             `json:"tools"`
	ResourcesSupported bool               `json:"resourcesSupported"`
	Resources          []Resource         `json:"resources"`
	ResourceTemplates  []ResourceTemplate `json:"resourceTemplates"`
	PromptsSupported   bool               `json:"promptsSupported"`
	Prompts            []Prompt           `json:"prompts"`
}

// Counts renders "3 tools, 2 resources, 1 prompt" for status lines.
func (s *Snapshot) Counts() string {
	plural := func(n int, word string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, word)
		}
		return fmt.Sprintf("%d %ss", n, word)
	}
	parts := []string{plural(len(s.Tools), "tool")}
	parts = append(parts, plural(len(s.Resources)+len(s.ResourceTemplates), "resource"))
	parts = append(parts, plural(len(s.Prompts), "prompt"))
	return strings.Join(parts, ", ")
}

// Normalize sorts every list and canonicalizes tool schemas in place,
// so two snapshots of the same surface are byte-identical after Encode
// no matter what order the server listed things in.
func (s *Snapshot) Normalize() error {
	s.Schema = SchemaVersion
	sort.Slice(s.Tools, func(i, j int) bool { return s.Tools[i].Name < s.Tools[j].Name })
	sort.Slice(s.Resources, func(i, j int) bool { return s.Resources[i].URI < s.Resources[j].URI })
	sort.Slice(s.ResourceTemplates, func(i, j int) bool {
		return s.ResourceTemplates[i].URITemplate < s.ResourceTemplates[j].URITemplate
	})
	sort.Slice(s.Prompts, func(i, j int) bool { return s.Prompts[i].Name < s.Prompts[j].Name })
	for i := range s.Tools {
		if len(s.Tools[i].InputSchema) == 0 {
			s.Tools[i].SchemaHash = ""
			continue
		}
		canon, err := CanonicalJSON(s.Tools[i].InputSchema)
		if err != nil {
			return fmt.Errorf("tool %s: invalid inputSchema: %w", s.Tools[i].Name, err)
		}
		s.Tools[i].InputSchema = canon
		s.Tools[i].SchemaHash = HashBytes(canon)
	}
	return nil
}

// CanonicalJSON re-encodes a JSON document deterministically: object
// keys sorted, no insignificant whitespace, numbers preserved verbatim
// via json.Number (so 1e2 does not silently become 100).
func CanonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	// encoding/json sorts map keys, which gives us canonical order.
	out, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// HashBytes returns the first 12 hex chars of SHA-256(b) — short enough
// to read in a diff line, long enough that collisions are irrelevant
// for a dev tool.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// Encode renders the snapshot as stable, indented JSON with a trailing
// newline; the caller must have Normalized it first.
func Encode(s *Snapshot) ([]byte, error) {
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Decode parses a snapshot produced by Encode (or hand-written to the
// same shape) and re-normalizes it, so `mcpwatch diff` accepts files
// regardless of their list order.
func Decode(data []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("invalid capability snapshot: %w", err)
	}
	if s.Schema > SchemaVersion {
		return nil, fmt.Errorf("capability snapshot uses schema_version %d; this mcpwatch understands up to %d", s.Schema, SchemaVersion)
	}
	if err := s.Normalize(); err != nil {
		return nil, err
	}
	return &s, nil
}

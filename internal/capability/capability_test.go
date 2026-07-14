// Tests for snapshot canonicalization — the property everything else
// leans on: the same server surface must serialize to the same bytes,
// and schema hashes must be stable under key reordering but sensitive
// to real changes.
package capability

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func sampleSnapshot() *Snapshot {
	return &Snapshot{
		ProtocolVersion: "2025-03-26",
		ServerInfo:      ServerInfo{Name: "demo", Version: "1.0.0"},
		ToolsSupported:  true,
		Tools: []Tool{
			{Name: "zeta", Description: "last", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "alpha", Description: "first", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		ResourcesSupported: true,
		Resources: []Resource{
			{URI: "notes://b"}, {URI: "notes://a"},
		},
		PromptsSupported: true,
		Prompts: []Prompt{
			{Name: "p", Arguments: []PromptArgument{{Name: "date", Required: true}, {Name: "style"}}},
		},
	}
}

func TestNormalizeSortsAndEncodeIsDeterministic(t *testing.T) {
	s := sampleSnapshot()
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	if s.Tools[0].Name != "alpha" || s.Tools[1].Name != "zeta" {
		t.Fatalf("tools not sorted: %v", s.Tools)
	}
	if s.Resources[0].URI != "notes://a" {
		t.Fatalf("resources not sorted: %v", s.Resources)
	}

	// Reversed input list order must not leak into the encoding.
	b := sampleSnapshot()
	b.Tools[0], b.Tools[1] = b.Tools[1], b.Tools[0]
	if err := b.Normalize(); err != nil {
		t.Fatal(err)
	}
	ea, _ := Encode(s)
	eb, _ := Encode(b)
	if !bytes.Equal(ea, eb) {
		t.Fatal("same surface must encode byte-identically")
	}
}

func TestSchemaHashIgnoresKeyOrderButDetectsRealChanges(t *testing.T) {
	a := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	b := json.RawMessage("{\n  \"properties\": {\"x\": {\"type\": \"string\"}},\n  \"type\": \"object\"\n}")
	ca, err := CanonicalJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := CanonicalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if HashBytes(ca) != HashBytes(cb) {
		t.Fatal("semantically identical schemas must hash equal")
	}

	x, _ := CanonicalJSON(json.RawMessage(`{"required":["title"]}`))
	y, _ := CanonicalJSON(json.RawMessage(`{"required":["title","body"]}`))
	if HashBytes(x) == HashBytes(y) {
		t.Fatal("different schemas must hash differently")
	}
}

func TestCanonicalJSONPreservesNumbersVerbatim(t *testing.T) {
	// float64 round-tripping would turn 1e999… style numbers into
	// garbage; json.Number keeps the schema byte-faithful.
	out, err := CanonicalJSON(json.RawMessage(`{"maximum":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "9007199254740993") {
		t.Fatalf("large integer mangled: %s", out)
	}
}

func TestNormalizeRejectsInvalidSchema(t *testing.T) {
	s := &Snapshot{Tools: []Tool{{Name: "bad", InputSchema: json.RawMessage(`{oops`)}}}
	if err := s.Normalize(); err == nil {
		t.Fatal("invalid inputSchema JSON must be an error")
	}
}

func TestDecodeRoundTripAndValidation(t *testing.T) {
	s := sampleSnapshot()
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	data, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerInfo != s.ServerInfo || len(got.Tools) != 2 || got.Tools[0].SchemaHash == "" {
		t.Fatalf("round trip lost data: %+v", got)
	}

	if _, err := Decode([]byte("not json")); err == nil {
		t.Fatal("garbage must be rejected")
	}
	if _, err := Decode([]byte(`{"schema_version":99}`)); err == nil {
		t.Fatal("a snapshot from a future mcpwatch must be refused, not misread")
	}
}

func TestHumanRenderingHelpers(t *testing.T) {
	p := Prompt{Arguments: []PromptArgument{{Name: "date", Required: true}, {Name: "style"}}}
	if got := p.Signature(); got != "date, style?" {
		t.Fatalf("Signature = %q", got)
	}
	if (Prompt{}).Signature() != "" {
		t.Fatal("no arguments should render empty")
	}
	if got := (ServerInfo{Name: "s", Version: "2.1"}).String(); got != "s 2.1" {
		t.Fatalf("String = %q", got)
	}
	if got := (ServerInfo{Name: "s"}).String(); got != "s" {
		t.Fatalf("versionless String = %q", got)
	}
	if got := sampleSnapshot().Counts(); got != "2 tools, 2 resources, 1 prompt" {
		t.Fatalf("Counts = %q", got)
	}
}

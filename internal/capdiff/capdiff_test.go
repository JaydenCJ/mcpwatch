// Tests for capability diffing — the feature mcpwatch exists for. Each
// case builds two normalized snapshots and asserts on the structured
// diff, so a wrong classification (added vs changed vs removed) fails
// loudly here rather than confusing a user mid-dev-loop.
package capdiff

import (
	"encoding/json"
	"testing"

	"github.com/JaydenCJ/mcpwatch/internal/capability"
)

// snap builds a normalized snapshot from parts, failing the test on
// schema errors so cases stay one-liners.
func snap(t *testing.T, mut func(*capability.Snapshot)) *capability.Snapshot {
	t.Helper()
	s := &capability.Snapshot{
		ProtocolVersion:    "2025-03-26",
		ServerInfo:         capability.ServerInfo{Name: "demo", Version: "1.0.0"},
		ToolsSupported:     true,
		ResourcesSupported: true,
		PromptsSupported:   true,
	}
	if mut != nil {
		mut(s)
	}
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	return s
}

func tool(name, desc, schema string) capability.Tool {
	tl := capability.Tool{Name: name, Description: desc}
	if schema != "" {
		tl.InputSchema = json.RawMessage(schema)
	}
	return tl
}

func TestIdenticalSnapshotsDiffEmpty(t *testing.T) {
	a := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("echo", "Echo", `{"type":"object"}`)}
	})
	b := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("echo", "Echo", `{"type":"object"}`)}
	})
	if d := Compute(a, b); !d.Empty() {
		t.Fatalf("identical surfaces must diff empty, got %+v", d)
	}
}

func TestAddedAndRemovedTools(t *testing.T) {
	a := snap(t, nil)
	b := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("slugify", "Make a slug", "")}
	})
	d := Compute(a, b)
	if len(d.Tools.Added) != 1 || d.Tools.Added[0].Name != "slugify" || d.Tools.Added[0].Note != "Make a slug" {
		t.Fatalf("added = %+v", d.Tools.Added)
	}
	if d.Tools.OldCount != 0 || d.Tools.NewCount != 1 {
		t.Fatalf("counts = %d → %d", d.Tools.OldCount, d.Tools.NewCount)
	}
	// The reverse direction is a removal.
	back := Compute(b, a)
	if len(back.Tools.Removed) != 1 || back.Tools.Removed[0].Name != "slugify" {
		t.Fatalf("removed = %+v", back.Tools.Removed)
	}
}

func TestToolSchemaChangeReportsBothHashes(t *testing.T) {
	a := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("add", "Add", `{"required":["x"]}`)}
	})
	b := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("add", "Add", `{"required":["x","y"]}`)}
	})
	d := Compute(a, b)
	if len(d.Tools.Changed) != 1 {
		t.Fatalf("changed = %+v", d.Tools.Changed)
	}
	detail := d.Tools.Changed[0].Details[0]
	wantOld, wantNew := a.Tools[0].SchemaHash, b.Tools[0].SchemaHash
	if detail != "input schema changed ("+wantOld+" → "+wantNew+")" {
		t.Fatalf("detail = %q", detail)
	}

	// Adding or dropping the schema entirely reads differently.
	none := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("t", "d", "")}
	})
	some := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("t", "d", `{"type":"object"}`)}
	})
	if d := Compute(none, some); d.Tools.Changed[0].Details[0] != "input schema added ("+some.Tools[0].SchemaHash+")" {
		t.Fatalf("schema-added detail = %v", d.Tools.Changed)
	}
	if d := Compute(some, none); d.Tools.Changed[0].Details[0] != "input schema removed" {
		t.Fatalf("schema-removed detail = %v", d.Tools.Changed)
	}
}

func TestToolDescriptionAndSchemaChangeBothListed(t *testing.T) {
	a := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("t", "old words", `{"a":1}`)}
	})
	b := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("t", "new words", `{"a":2}`)}
	})
	d := Compute(a, b)
	if len(d.Tools.Changed) != 1 || len(d.Tools.Changed[0].Details) != 2 {
		t.Fatalf("want both details on one change entry, got %+v", d.Tools.Changed)
	}
}

func TestResourcesKeyedByURI(t *testing.T) {
	a := snap(t, func(s *capability.Snapshot) {
		s.Resources = []capability.Resource{{URI: "notes://today", Name: "Today", MIMEType: "text/plain"}}
	})
	b := snap(t, func(s *capability.Snapshot) {
		s.Resources = []capability.Resource{{URI: "notes://today", Name: "Today", MIMEType: "text/markdown"}}
	})
	d := Compute(a, b)
	if len(d.Resources.Changed) != 1 || d.Resources.Changed[0].Name != "notes://today" {
		t.Fatalf("changed = %+v", d.Resources.Changed)
	}
	if d.Resources.Changed[0].Details[0] != "mimeType changed (text/plain → text/markdown)" {
		t.Fatalf("detail = %q", d.Resources.Changed[0].Details[0])
	}
}

func TestResourceTemplatesDiffSeparately(t *testing.T) {
	a := snap(t, nil)
	b := snap(t, func(s *capability.Snapshot) {
		s.ResourceTemplates = []capability.ResourceTemplate{{URITemplate: "notes://{date}", Name: "By date"}}
	})
	d := Compute(a, b)
	if len(d.ResourceTemplates.Added) != 1 || d.ResourceTemplates.Added[0].Name != "notes://{date}" {
		t.Fatalf("templates added = %+v", d.ResourceTemplates.Added)
	}
	if !d.Resources.Empty() {
		t.Fatal("template change must not leak into the resources section")
	}
}

func TestPromptArgumentChangeUsesSignature(t *testing.T) {
	a := snap(t, func(s *capability.Snapshot) {
		s.Prompts = []capability.Prompt{{Name: "review", Arguments: []capability.PromptArgument{{Name: "path", Required: true}}}}
	})
	b := snap(t, func(s *capability.Snapshot) {
		s.Prompts = []capability.Prompt{{Name: "review", Arguments: []capability.PromptArgument{{Name: "path", Required: true}, {Name: "style"}}}}
	})
	d := Compute(a, b)
	if d.Prompts.Changed[0].Details[0] != "arguments changed (path → path, style?)" {
		t.Fatalf("detail = %q", d.Prompts.Changed[0].Details[0])
	}
}

func TestServerAndProtocolChanges(t *testing.T) {
	a := snap(t, nil)
	b := snap(t, func(s *capability.Snapshot) {
		s.ServerInfo.Version = "2.0.0"
		s.ProtocolVersion = "2024-11-05"
	})
	d := Compute(a, b)
	if d.Server == nil || d.Server.New != "demo 2.0.0" {
		t.Fatalf("server change = %+v", d.Server)
	}
	if d.Protocol == nil || d.Protocol.Old != "2025-03-26" {
		t.Fatalf("protocol change = %+v", d.Protocol)
	}
}

func TestSectionSupportTransitions(t *testing.T) {
	a := snap(t, func(s *capability.Snapshot) { s.PromptsSupported = false })
	b := snap(t, func(s *capability.Snapshot) {
		s.Prompts = []capability.Prompt{{Name: "p"}}
	})
	d := Compute(a, b)
	if !d.Prompts.GainedSupport {
		t.Fatalf("want GainedSupport, got %+v", d.Prompts)
	}
	back := Compute(b, a)
	if !back.Prompts.LostSupport {
		t.Fatalf("want LostSupport, got %+v", back.Prompts)
	}
}

func TestDiffOrderingIsDeterministic(t *testing.T) {
	a := snap(t, nil)
	b := snap(t, func(s *capability.Snapshot) {
		s.Tools = []capability.Tool{tool("zz", "", ""), tool("aa", "", ""), tool("mm", "", "")}
	})
	d := Compute(a, b)
	if d.Tools.Added[0].Name != "aa" || d.Tools.Added[2].Name != "zz" {
		t.Fatalf("added must be name-sorted, got %+v", d.Tools.Added)
	}
}

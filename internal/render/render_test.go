// Tests for terminal rendering. These assert on exact lines where the
// format is a contract (diff markers, section headers) and on presence
// where only content matters — so cosmetic tweaks stay cheap but the
// grep-able surface stays stable.
package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaydenCJ/mcpwatch/internal/capability"
	"github.com/JaydenCJ/mcpwatch/internal/capdiff"
)

func normalized(t *testing.T, s *capability.Snapshot) *capability.Snapshot {
	t.Helper()
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	return s
}

func demoSnap(t *testing.T) *capability.Snapshot {
	return normalized(t, &capability.Snapshot{
		ProtocolVersion:    "2025-03-26",
		ServerInfo:         capability.ServerInfo{Name: "demo-notes", Version: "1.0.0"},
		ToolsSupported:     true,
		ResourcesSupported: true,
		PromptsSupported:   true,
		Tools: []capability.Tool{
			{Name: "echo", Description: "Echo a message", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "add_note", Description: "Create a note"},
		},
		Resources: []capability.Resource{{URI: "notes://today", Name: "Today", MIMEType: "text/markdown"}},
		Prompts:   []capability.Prompt{{Name: "summarize", Description: "Summarize notes", Arguments: []capability.PromptArgument{{Name: "date", Required: true}}}},
	})
}

func TestSnapshotHeaderSectionsAndCounts(t *testing.T) {
	var b strings.Builder
	Snapshot(&b, demoSnap(t))
	out := b.String()
	if !strings.HasPrefix(out, "demo-notes 1.0.0 — protocol 2025-03-26\n") {
		t.Fatalf("header wrong:\n%s", out)
	}
	for _, want := range []string{"tools (2)", "resources (1)", "prompts (1)", "summarize(date)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestSnapshotAlignsToolColumns(t *testing.T) {
	var b strings.Builder
	Snapshot(&b, demoSnap(t))
	// Both descriptions must start in the same column.
	var cols []int
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.Contains(line, "Echo a message") || strings.Contains(line, "Create a note") {
			cols = append(cols, strings.Index(line, "E")+strings.Index(line, "C")+1) // one is -1
		}
	}
	if len(cols) != 2 || cols[0] != cols[1] {
		t.Fatalf("descriptions not aligned: %v\n%s", cols, b.String())
	}
}

func TestSnapshotUnsupportedVsEmptySections(t *testing.T) {
	// "not supported" (section undeclared) and "(none)" (declared but
	// empty) are different situations and must read differently.
	unsupported := normalized(t, &capability.Snapshot{
		ServerInfo:     capability.ServerInfo{Name: "toolless"},
		ToolsSupported: false,
	})
	var b strings.Builder
	Snapshot(&b, unsupported)
	if !strings.Contains(b.String(), "tools: not supported") {
		t.Fatalf("unsupported marker missing:\n%s", b.String())
	}

	empty := normalized(t, &capability.Snapshot{
		ServerInfo:     capability.ServerInfo{Name: "empty"},
		ToolsSupported: true,
	})
	b.Reset()
	Snapshot(&b, empty)
	if !strings.Contains(b.String(), "tools (0)\n  (none)") {
		t.Fatalf("empty section rendering wrong:\n%s", b.String())
	}
}

func TestSnapshotTruncatesLongDescriptions(t *testing.T) {
	long := strings.Repeat("very long description ", 10)
	s := normalized(t, &capability.Snapshot{
		ToolsSupported: true,
		Tools:          []capability.Tool{{Name: "wordy", Description: long}},
	})
	var b strings.Builder
	Snapshot(&b, s)
	if !strings.Contains(b.String(), "…") {
		t.Fatal("long description should be truncated with an ellipsis")
	}
	for _, line := range strings.Split(b.String(), "\n") {
		if len([]rune(line)) > 100 {
			t.Fatalf("line too long after truncation: %q", line)
		}
	}
}

func TestDiffMarkers(t *testing.T) {
	d := &capdiff.Diff{
		Tools: capdiff.Section{
			OldCount: 2, NewCount: 3,
			Added:   []capdiff.Entry{{Name: "slugify", Note: "Make a slug"}},
			Changed: []capdiff.Change{{Name: "add", Details: []string{"input schema changed (aaa → bbb)"}}},
			Removed: []capdiff.Entry{{Name: "legacy"}},
		},
	}
	var b strings.Builder
	Diff(&b, d)
	out := b.String()
	for _, want := range []string{
		"tools 2 → 3",
		"+ slugify",
		"~ add",
		"- legacy",
		"input schema changed (aaa → bbb)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDiffBannerAndSupportTransitionLines(t *testing.T) {
	var b strings.Builder
	Diff(&b, &capdiff.Diff{Prompts: capdiff.Section{GainedSupport: true, NewCount: 2}})
	if !strings.Contains(b.String(), "prompts: now supported (2)") {
		t.Fatalf("gained-support wording wrong:\n%s", b.String())
	}
	b.Reset()
	Diff(&b, &capdiff.Diff{Prompts: capdiff.Section{LostSupport: true, OldCount: 2}})
	if !strings.Contains(b.String(), "prompts: no longer supported (was 2)") {
		t.Fatalf("lost-support wording wrong:\n%s", b.String())
	}
	b.Reset()
	Diff(&b, &capdiff.Diff{
		Server:   &capdiff.FieldChange{Old: "demo 1.0.0", New: "demo 1.1.0"},
		Protocol: &capdiff.FieldChange{Old: "2024-11-05", New: "2025-03-26"},
	})
	if !strings.Contains(b.String(), "server: demo 1.0.0 → demo 1.1.0") ||
		!strings.Contains(b.String(), "protocol: 2024-11-05 → 2025-03-26") {
		t.Fatalf("banner lines wrong:\n%s", b.String())
	}
}

func TestDiffEmptyPrintsNothing(t *testing.T) {
	var b strings.Builder
	Diff(&b, &capdiff.Diff{})
	if b.Len() != 0 {
		t.Fatalf("empty diff must render nothing, got %q", b.String())
	}
}

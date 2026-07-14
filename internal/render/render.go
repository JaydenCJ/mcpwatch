// Package render turns capability snapshots and diffs into the terminal
// text mcpwatch prints. Output is plain UTF-8 with no ANSI colors —
// dev-loop output gets piped, grepped, and pasted into issues, and
// color codes ruin all three.
package render

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/JaydenCJ/mcpwatch/internal/capability"
	"github.com/JaydenCJ/mcpwatch/internal/capdiff"
)

// maxNote caps descriptions in listings so one wordy tool cannot wrap
// the whole table.
const maxNote = 72

// Snapshot writes the full capability surface as text.
func Snapshot(w io.Writer, s *capability.Snapshot) {
	fmt.Fprintf(w, "%s — protocol %s\n", s.ServerInfo.String(), s.ProtocolVersion)

	writeSection(w, "tools", s.ToolsSupported, len(s.Tools), func() {
		width := 0
		for _, t := range s.Tools {
			width = max(width, utf8.RuneCountInString(t.Name))
		}
		for _, t := range s.Tools {
			fmt.Fprintf(w, "  %-*s  %s\n", width, t.Name, truncate(t.Description))
		}
	})

	writeSection(w, "resources", s.ResourcesSupported, len(s.Resources), func() {
		width := 0
		for _, r := range s.Resources {
			width = max(width, utf8.RuneCountInString(r.URI))
		}
		for _, r := range s.Resources {
			note := joinNonEmpty(r.Name, r.MIMEType)
			fmt.Fprintf(w, "  %-*s  %s\n", width, r.URI, truncate(note))
		}
	})

	if len(s.ResourceTemplates) > 0 {
		fmt.Fprintf(w, "\nresource templates (%d)\n", len(s.ResourceTemplates))
		width := 0
		for _, t := range s.ResourceTemplates {
			width = max(width, utf8.RuneCountInString(t.URITemplate))
		}
		for _, t := range s.ResourceTemplates {
			note := joinNonEmpty(t.Name, t.MIMEType)
			fmt.Fprintf(w, "  %-*s  %s\n", width, t.URITemplate, truncate(note))
		}
	}

	writeSection(w, "prompts", s.PromptsSupported, len(s.Prompts), func() {
		names := make([]string, len(s.Prompts))
		width := 0
		for i, p := range s.Prompts {
			names[i] = p.Name
			if sig := p.Signature(); sig != "" {
				names[i] += "(" + sig + ")"
			}
			width = max(width, utf8.RuneCountInString(names[i]))
		}
		for i, p := range s.Prompts {
			fmt.Fprintf(w, "  %-*s  %s\n", width, names[i], truncate(p.Description))
		}
	})
}

func writeSection(w io.Writer, label string, supported bool, n int, body func()) {
	if !supported {
		fmt.Fprintf(w, "\n%s: not supported\n", label)
		return
	}
	fmt.Fprintf(w, "\n%s (%d)\n", label, n)
	if n == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	body()
}

// Diff writes the capability diff as text. It prints nothing when the
// diff is empty — the caller owns the "no capability changes" line so
// it can attach restart context.
func Diff(w io.Writer, d *capdiff.Diff) {
	if d.Server != nil {
		fmt.Fprintf(w, "server: %s → %s\n", d.Server.Old, d.Server.New)
	}
	if d.Protocol != nil {
		fmt.Fprintf(w, "protocol: %s → %s\n", d.Protocol.Old, d.Protocol.New)
	}
	diffSection(w, "tools", d.Tools)
	diffSection(w, "resources", d.Resources)
	diffSection(w, "resource templates", d.ResourceTemplates)
	diffSection(w, "prompts", d.Prompts)
}

func diffSection(w io.Writer, label string, s capdiff.Section) {
	if s.Empty() {
		return
	}
	switch {
	case s.GainedSupport:
		fmt.Fprintf(w, "%s: now supported (%d)\n", label, s.NewCount)
	case s.LostSupport:
		fmt.Fprintf(w, "%s: no longer supported (was %d)\n", label, s.OldCount)
	default:
		fmt.Fprintf(w, "%s %d → %d\n", label, s.OldCount, s.NewCount)
	}
	width := 0
	for _, e := range s.Added {
		width = max(width, utf8.RuneCountInString(e.Name))
	}
	for _, c := range s.Changed {
		width = max(width, utf8.RuneCountInString(c.Name))
	}
	for _, e := range s.Removed {
		width = max(width, utf8.RuneCountInString(e.Name))
	}
	for _, e := range s.Added {
		fmt.Fprintf(w, "  + %-*s  %s\n", width, e.Name, truncate(e.Note))
	}
	for _, c := range s.Changed {
		fmt.Fprintf(w, "  ~ %-*s  %s\n", width, c.Name, strings.Join(c.Details, "; "))
	}
	for _, e := range s.Removed {
		fmt.Fprintf(w, "  - %s\n", e.Name)
	}
}

func truncate(s string) string {
	if utf8.RuneCountInString(s) <= maxNote {
		return strings.TrimRight(s, " ")
	}
	runes := []rune(s)
	return strings.TrimRight(string(runes[:maxNote-1]), " ") + "…"
}

func joinNonEmpty(parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "  ")
}

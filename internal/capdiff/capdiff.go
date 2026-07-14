// Package capdiff computes the semantic difference between two
// capability snapshots: which tools, resources, resource templates, and
// prompts appeared, disappeared, or changed shape between server runs.
// The result is a plain data structure so the same diff can be rendered
// as terminal text or emitted as JSON for scripts.
package capdiff

import (
	"fmt"
	"sort"

	"github.com/JaydenCJ/mcpwatch/internal/capability"
)

// FieldChange records an old → new transition of a scalar field.
type FieldChange struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// Entry names an added or removed item, with a short human note (the
// description for tools/prompts, the name for resources).
type Entry struct {
	Name string `json:"name"`
	Note string `json:"note,omitempty"`
}

// Change records an item present on both sides whose shape changed,
// with one human-readable detail per changed aspect.
type Change struct {
	Name    string   `json:"name"`
	Details []string `json:"details"`
}

// Section is the diff of one capability list. GainedSupport/LostSupport
// flag the section appearing or vanishing wholesale (a server that
// stopped declaring prompts, say), which is more newsworthy than any
// per-item change.
type Section struct {
	OldCount      int      `json:"oldCount"`
	NewCount      int      `json:"newCount"`
	GainedSupport bool     `json:"gainedSupport,omitempty"`
	LostSupport   bool     `json:"lostSupport,omitempty"`
	Added         []Entry  `json:"added,omitempty"`
	Removed       []Entry  `json:"removed,omitempty"`
	Changed       []Change `json:"changed,omitempty"`
}

// Empty reports whether this section is unchanged.
func (s Section) Empty() bool {
	return !s.GainedSupport && !s.LostSupport &&
		len(s.Added) == 0 && len(s.Removed) == 0 && len(s.Changed) == 0
}

// Diff is the full comparison of two snapshots.
type Diff struct {
	Server            *FieldChange `json:"server,omitempty"`
	Protocol          *FieldChange `json:"protocol,omitempty"`
	Tools             Section      `json:"tools"`
	Resources         Section      `json:"resources"`
	ResourceTemplates Section      `json:"resourceTemplates"`
	Prompts           Section      `json:"prompts"`
}

// Empty reports whether the two snapshots describe the same surface.
func (d *Diff) Empty() bool {
	return d.Server == nil && d.Protocol == nil &&
		d.Tools.Empty() && d.Resources.Empty() &&
		d.ResourceTemplates.Empty() && d.Prompts.Empty()
}

// Compute diffs old against cur. Both snapshots must be normalized
// (sorted, schemas hashed) — mcpclient and capability.Decode guarantee
// that for every snapshot mcpwatch produces.
func Compute(old, cur *capability.Snapshot) *Diff {
	d := &Diff{}
	if old.ServerInfo != cur.ServerInfo {
		d.Server = &FieldChange{Old: old.ServerInfo.String(), New: cur.ServerInfo.String()}
	}
	if old.ProtocolVersion != cur.ProtocolVersion {
		d.Protocol = &FieldChange{Old: old.ProtocolVersion, New: cur.ProtocolVersion}
	}
	d.Tools = diffTools(old, cur)
	d.Resources = diffResources(old, cur)
	d.ResourceTemplates = diffTemplates(old, cur)
	d.Prompts = diffPrompts(old, cur)
	return d
}

func diffTools(old, cur *capability.Snapshot) Section {
	s := sectionHeader(len(old.Tools), len(cur.Tools), old.ToolsSupported, cur.ToolsSupported)
	oldBy := make(map[string]capability.Tool, len(old.Tools))
	for _, t := range old.Tools {
		oldBy[t.Name] = t
	}
	seen := make(map[string]bool, len(cur.Tools))
	for _, t := range cur.Tools {
		seen[t.Name] = true
		prev, ok := oldBy[t.Name]
		if !ok {
			s.Added = append(s.Added, Entry{Name: t.Name, Note: t.Description})
			continue
		}
		var details []string
		if prev.Description != t.Description {
			details = append(details, "description changed")
		}
		switch {
		case prev.SchemaHash == t.SchemaHash:
			// unchanged
		case prev.SchemaHash == "":
			details = append(details, fmt.Sprintf("input schema added (%s)", t.SchemaHash))
		case t.SchemaHash == "":
			details = append(details, "input schema removed")
		default:
			details = append(details, fmt.Sprintf("input schema changed (%s → %s)", prev.SchemaHash, t.SchemaHash))
		}
		if len(details) > 0 {
			s.Changed = append(s.Changed, Change{Name: t.Name, Details: details})
		}
	}
	for _, t := range old.Tools {
		if !seen[t.Name] {
			s.Removed = append(s.Removed, Entry{Name: t.Name})
		}
	}
	sortSection(&s)
	return s
}

func diffResources(old, cur *capability.Snapshot) Section {
	s := sectionHeader(len(old.Resources), len(cur.Resources), old.ResourcesSupported, cur.ResourcesSupported)
	oldBy := make(map[string]capability.Resource, len(old.Resources))
	for _, r := range old.Resources {
		oldBy[r.URI] = r
	}
	seen := make(map[string]bool, len(cur.Resources))
	for _, r := range cur.Resources {
		seen[r.URI] = true
		prev, ok := oldBy[r.URI]
		if !ok {
			s.Added = append(s.Added, Entry{Name: r.URI, Note: r.Name})
			continue
		}
		var details []string
		if prev.Name != r.Name {
			details = append(details, fmt.Sprintf("name changed (%q → %q)", prev.Name, r.Name))
		}
		if prev.MIMEType != r.MIMEType {
			details = append(details, fmt.Sprintf("mimeType changed (%s → %s)", orDash(prev.MIMEType), orDash(r.MIMEType)))
		}
		if prev.Description != r.Description {
			details = append(details, "description changed")
		}
		if len(details) > 0 {
			s.Changed = append(s.Changed, Change{Name: r.URI, Details: details})
		}
	}
	for _, r := range old.Resources {
		if !seen[r.URI] {
			s.Removed = append(s.Removed, Entry{Name: r.URI})
		}
	}
	sortSection(&s)
	return s
}

func diffTemplates(old, cur *capability.Snapshot) Section {
	// Templates ride on the resources capability, so support flags are
	// shared with resources and not repeated here.
	s := Section{OldCount: len(old.ResourceTemplates), NewCount: len(cur.ResourceTemplates)}
	oldBy := make(map[string]capability.ResourceTemplate, len(old.ResourceTemplates))
	for _, t := range old.ResourceTemplates {
		oldBy[t.URITemplate] = t
	}
	seen := make(map[string]bool, len(cur.ResourceTemplates))
	for _, t := range cur.ResourceTemplates {
		seen[t.URITemplate] = true
		prev, ok := oldBy[t.URITemplate]
		if !ok {
			s.Added = append(s.Added, Entry{Name: t.URITemplate, Note: t.Name})
			continue
		}
		var details []string
		if prev.Name != t.Name {
			details = append(details, fmt.Sprintf("name changed (%q → %q)", prev.Name, t.Name))
		}
		if prev.MIMEType != t.MIMEType {
			details = append(details, fmt.Sprintf("mimeType changed (%s → %s)", orDash(prev.MIMEType), orDash(t.MIMEType)))
		}
		if prev.Description != t.Description {
			details = append(details, "description changed")
		}
		if len(details) > 0 {
			s.Changed = append(s.Changed, Change{Name: t.URITemplate, Details: details})
		}
	}
	for _, t := range old.ResourceTemplates {
		if !seen[t.URITemplate] {
			s.Removed = append(s.Removed, Entry{Name: t.URITemplate})
		}
	}
	sortSection(&s)
	return s
}

func diffPrompts(old, cur *capability.Snapshot) Section {
	s := sectionHeader(len(old.Prompts), len(cur.Prompts), old.PromptsSupported, cur.PromptsSupported)
	oldBy := make(map[string]capability.Prompt, len(old.Prompts))
	for _, p := range old.Prompts {
		oldBy[p.Name] = p
	}
	seen := make(map[string]bool, len(cur.Prompts))
	for _, p := range cur.Prompts {
		seen[p.Name] = true
		prev, ok := oldBy[p.Name]
		if !ok {
			s.Added = append(s.Added, Entry{Name: p.Name, Note: p.Description})
			continue
		}
		var details []string
		if prev.Description != p.Description {
			details = append(details, "description changed")
		}
		if prev.Signature() != p.Signature() {
			details = append(details, fmt.Sprintf("arguments changed (%s → %s)", orDash(prev.Signature()), orDash(p.Signature())))
		}
		if len(details) > 0 {
			s.Changed = append(s.Changed, Change{Name: p.Name, Details: details})
		}
	}
	for _, p := range old.Prompts {
		if !seen[p.Name] {
			s.Removed = append(s.Removed, Entry{Name: p.Name})
		}
	}
	sortSection(&s)
	return s
}

func sectionHeader(oldN, newN int, oldSup, newSup bool) Section {
	return Section{
		OldCount:      oldN,
		NewCount:      newN,
		GainedSupport: newSup && !oldSup,
		LostSupport:   oldSup && !newSup,
	}
}

// sortSection keeps output deterministic even though the maps above
// iterate in random order.
func sortSection(s *Section) {
	sort.Slice(s.Added, func(i, j int) bool { return s.Added[i].Name < s.Added[j].Name })
	sort.Slice(s.Removed, func(i, j int) bool { return s.Removed[i].Name < s.Removed[j].Name })
	sort.Slice(s.Changed, func(i, j int) bool { return s.Changed[i].Name < s.Changed[j].Name })
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

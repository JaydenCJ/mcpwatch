// Package watch implements mcpwatch's change detection as pure snapshot
// arithmetic: Scan captures the state of a file tree, Diff compares two
// snapshots, and Debouncer decides when a burst of edits has settled.
// Nothing in this package sleeps or spawns goroutines — the polling loop
// that drives it lives in the CLI, which keeps every piece deterministic
// and unit-testable.
package watch

import (
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JaydenCJ/mcpwatch/internal/glob"
)

// DefaultHashLimit is the largest file (in bytes) whose content is
// hashed. Larger files fall back to size + mtime comparison.
const DefaultHashLimit = 8 << 20

// DefaultExcludes are pruned from every scan unless the user opts out.
// They cover the build/VCS noise that would otherwise cause pointless
// server restarts.
var DefaultExcludes = []string{
	".git/**", ".hg/**", ".svn/**",
	"node_modules/**", "vendor/**", "target/**",
	"dist/**", "build/**", "out/**",
	"__pycache__/**", ".venv/**", ".mypy_cache/**", ".pytest_cache/**",
	"*.log", "*.tmp", "*.swp", ".DS_Store",
}

// FileState is the identity of one file at scan time. Two states are
// considered equal when size and content hash agree; the modification
// time only matters for files too large to hash.
type FileState struct {
	Size    int64
	ModTime int64 // unix nanoseconds
	Hash    uint64
	Hashed  bool
}

func (a FileState) equal(b FileState) bool {
	if a.Size != b.Size {
		return false
	}
	if a.Hashed && b.Hashed {
		return a.Hash == b.Hash
	}
	return a.ModTime == b.ModTime
}

// Snapshot maps a display path (root-joined, forward slashes) to the
// file's state.
type Snapshot map[string]FileState

// Config controls a scan.
type Config struct {
	// Roots are the files or directories to watch.
	Roots []string
	// Include, when non-empty, restricts the snapshot to files matching
	// at least one pattern (relative to their root).
	Include []string
	// Exclude removes matching files; directories matching a pattern
	// are pruned without descending.
	Exclude []string
	// HashLimit caps content hashing; 0 means DefaultHashLimit and a
	// negative value disables hashing entirely.
	HashLimit int64
}

func (c Config) hashLimit() int64 {
	switch {
	case c.HashLimit == 0:
		return DefaultHashLimit
	case c.HashLimit < 0:
		return -1
	default:
		return c.HashLimit
	}
}

// Scan walks every root and returns the tree's current snapshot.
// Files that vanish mid-walk are skipped rather than failing the scan,
// since editors routinely replace files with delete+rename.
func (c Config) Scan() (Snapshot, error) {
	snap := make(Snapshot)
	limit := c.hashLimit()
	for _, root := range c.Roots {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("watch root %s: %w", root, err)
		}
		if !info.IsDir() {
			// A root can be a single file (e.g. one config).
			base := filepath.ToSlash(filepath.Base(root))
			if c.keep(base) {
				addFile(snap, filepath.ToSlash(filepath.Clean(root)), info, root, limit)
			}
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil // deleted mid-walk
				}
				return err
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil || rel == "." {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if glob.MatchAny(c.excludes(), rel) {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !c.keep(rel) {
				return nil
			}
			fi, serr := d.Info()
			if serr != nil {
				return nil // deleted mid-walk
			}
			addFile(snap, filepath.ToSlash(filepath.Join(root, rel)), fi, path, limit)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("watch root %s: %w", root, err)
		}
	}
	return snap, nil
}

func (c Config) excludes() []string {
	if c.Exclude == nil {
		return DefaultExcludes
	}
	return c.Exclude
}

func (c Config) keep(rel string) bool {
	if glob.MatchAny(c.excludes(), rel) {
		return false
	}
	if len(c.Include) == 0 {
		return true
	}
	return glob.MatchAny(c.Include, rel)
}

func addFile(snap Snapshot, key string, info fs.FileInfo, path string, hashLimit int64) {
	st := FileState{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
	if hashLimit >= 0 && info.Size() <= hashLimit {
		if h, err := hashFile(path); err == nil {
			st.Hash, st.Hashed = h, true
		}
	}
	snap[key] = st
}

func hashFile(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	h := fnv.New64a()
	if _, err := io.Copy(h, f); err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}

// Changes is the outcome of comparing two snapshots. All slices are
// sorted, so identical inputs always produce identical output.
type Changes struct {
	Added    []string
	Removed  []string
	Modified []string
}

// Empty reports whether nothing changed.
func (c Changes) Empty() bool {
	return len(c.Added) == 0 && len(c.Removed) == 0 && len(c.Modified) == 0
}

// Paths returns every touched path, sorted, for compact logging.
func (c Changes) Paths() []string {
	out := make([]string, 0, len(c.Added)+len(c.Removed)+len(c.Modified))
	out = append(out, c.Added...)
	out = append(out, c.Removed...)
	out = append(out, c.Modified...)
	sort.Strings(out)
	return out
}

// Summary renders e.g. "2 modified, 1 added" for the restart banner.
func (c Changes) Summary() string {
	var parts []string
	if n := len(c.Modified); n > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", n))
	}
	if n := len(c.Added); n > 0 {
		parts = append(parts, fmt.Sprintf("%d added", n))
	}
	if n := len(c.Removed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", n))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

// Diff compares two snapshots.
func Diff(old, cur Snapshot) Changes {
	var ch Changes
	for path, st := range cur {
		prev, ok := old[path]
		switch {
		case !ok:
			ch.Added = append(ch.Added, path)
		case !prev.equal(st):
			ch.Modified = append(ch.Modified, path)
		}
	}
	for path := range old {
		if _, ok := cur[path]; !ok {
			ch.Removed = append(ch.Removed, path)
		}
	}
	sort.Strings(ch.Added)
	sort.Strings(ch.Removed)
	sort.Strings(ch.Modified)
	return ch
}

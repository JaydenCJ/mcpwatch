// Tests for snapshot scanning and diffing. Change detection is content-
// hash based, so every case below is deterministic — no mtime races, no
// sleeps — which is the whole reason watch is built as pure snapshot
// arithmetic.
package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// writeFile creates path (and parents) with the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, cfg Config) Snapshot {
	t.Helper()
	snap, err := cfg.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return snap
}

func TestScanFindsRegularFilesAcrossRoots(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main")
	writeFile(t, filepath.Join(dir, "sub", "util.go"), "package sub")

	snap := scan(t, Config{Roots: []string{dir}})
	if len(snap) != 2 {
		t.Fatalf("want 2 files, got %d: %v", len(snap), snap)
	}

	// A second root contributes to the same snapshot.
	other := t.TempDir()
	writeFile(t, filepath.Join(other, "b.go"), "y")
	snap = scan(t, Config{Roots: []string{dir, other}})
	if len(snap) != 3 {
		t.Fatalf("both roots should contribute, got %v", snap)
	}

	// A root may also be a single file (e.g. one config).
	file := filepath.Join(t.TempDir(), "caps.json")
	writeFile(t, file, "{}")
	snap = scan(t, Config{Roots: []string{file}})
	if len(snap) != 1 {
		t.Fatalf("want exactly the file itself, got %v", snap)
	}
}

func TestScanMissingRootFails(t *testing.T) {
	_, err := Config{Roots: []string{filepath.Join(t.TempDir(), "nope")}}.Scan()
	if err == nil {
		t.Fatal("a missing watch root must be a hard error, not silence")
	}
}

func TestExcludeAndIncludeFiltering(t *testing.T) {
	// Default excludes prune VCS/build noise without configuration.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main")
	writeFile(t, filepath.Join(dir, ".git", "objects", "ab"), "x")
	writeFile(t, filepath.Join(dir, "node_modules", "pkg", "index.js"), "x")
	writeFile(t, filepath.Join(dir, "server.log"), "noise")
	snap := scan(t, Config{Roots: []string{dir}})
	if len(snap) != 1 {
		t.Fatalf("default excludes should leave only main.go, got %v", snap)
	}

	// Explicit excludes filter what defaults would keep.
	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "a.go"), "x")
	writeFile(t, filepath.Join(dir2, "b.md"), "x")
	snap = scan(t, Config{Roots: []string{dir2}, Exclude: []string{"*.md"}})
	if len(snap) != 1 {
		t.Fatalf("want only a.go, got %v", snap)
	}

	// Includes restrict to matching files at any depth.
	dir3 := t.TempDir()
	writeFile(t, filepath.Join(dir3, "a.go"), "x")
	writeFile(t, filepath.Join(dir3, "sub", "b.go"), "x")
	writeFile(t, filepath.Join(dir3, "README.md"), "x")
	snap = scan(t, Config{Roots: []string{dir3}, Include: []string{"*.go"}})
	if len(snap) != 2 {
		t.Fatalf("include *.go should keep both .go files, got %v", snap)
	}
}

func TestDiffDetectsAddedRemovedModified(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "keep.go"), "same")
	writeFile(t, filepath.Join(dir, "edit.go"), "old body")
	writeFile(t, filepath.Join(dir, "gone.go"), "doomed")
	cfg := Config{Roots: []string{dir}}
	before := scan(t, cfg)

	writeFile(t, filepath.Join(dir, "edit.go"), "new body!")
	writeFile(t, filepath.Join(dir, "fresh.go"), "hello")
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}
	after := scan(t, cfg)

	ch := Diff(before, after)
	rel := func(name string) string { return filepath.ToSlash(filepath.Join(dir, name)) }
	if !reflect.DeepEqual(ch.Added, []string{rel("fresh.go")}) {
		t.Fatalf("added = %v", ch.Added)
	}
	if !reflect.DeepEqual(ch.Removed, []string{rel("gone.go")}) {
		t.Fatalf("removed = %v", ch.Removed)
	}
	if !reflect.DeepEqual(ch.Modified, []string{rel("edit.go")}) {
		t.Fatalf("modified = %v", ch.Modified)
	}
}

func TestDiffOutputIsSorted(t *testing.T) {
	old := Snapshot{}
	cur := Snapshot{
		"z.go": {Size: 1, Hashed: true, Hash: 1},
		"a.go": {Size: 1, Hashed: true, Hash: 1},
		"m.go": {Size: 1, Hashed: true, Hash: 1},
	}
	ch := Diff(old, cur)
	if !reflect.DeepEqual(ch.Added, []string{"a.go", "m.go", "z.go"}) {
		t.Fatalf("added must be sorted, got %v", ch.Added)
	}
}

func TestRewriteWithSameContentIsNotAChange(t *testing.T) {
	// Editors and build tools love rewriting files byte-for-byte;
	// hashing means that must not trigger a server restart.
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	writeFile(t, path, "package main")
	cfg := Config{Roots: []string{dir}}
	before := scan(t, cfg)

	writeFile(t, path, "package main") // same bytes, new mtime
	after := scan(t, cfg)

	if ch := Diff(before, after); !ch.Empty() {
		t.Fatalf("byte-identical rewrite should be quiet, got %+v", ch)
	}
}

func TestUnhashedFilesFallBackToMtime(t *testing.T) {
	// With hashing disabled (HashLimit < 0) mtime+size is the identity;
	// pin the mtimes with Chtimes so the test stays deterministic.
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	writeFile(t, path, "0123456789")
	t0 := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, t0, t0); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Roots: []string{dir}, HashLimit: -1}
	before := scan(t, cfg)

	// Same size, different pinned mtime → must count as modified.
	if err := os.Chtimes(path, t0.Add(time.Second), t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	after := scan(t, cfg)
	if ch := Diff(before, after); len(ch.Modified) != 1 {
		t.Fatalf("mtime change should be modified without hashing, got %+v", ch)
	}
}

func TestChangesSummaryAndPaths(t *testing.T) {
	ch := Changes{Added: []string{"b"}, Modified: []string{"a", "c"}}
	if got := ch.Summary(); got != "2 modified, 1 added" {
		t.Fatalf("Summary = %q", got)
	}
	if got := ch.Paths(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("Paths = %v", got)
	}
	if (Changes{}).Summary() != "no changes" {
		t.Fatal("empty summary wording changed")
	}
}

// Tests for the glob dialect. The cases mirror the patterns users
// actually pass to --include/--exclude, so a regression here means a
// server restarting on files it should ignore (or worse, not
// restarting at all).
package glob

import "testing"

func TestSegmentWildcards(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
		why           string
	}{
		{"src/*.go", "src/main.go", true, "* matches within one segment"},
		{"src/*.go", "src/sub/main.go", false, "* must not cross a path separator"},
		{"v?.txt", "v1.txt", true, "? matches one character"},
		{"v?.txt", "v12.txt", false, "? must not match two characters"},
		{"v?.txt", "v.txt", false, "? must not match zero characters"},
		{"go.mod", "go.mod", true, "literals match themselves"},
		{"go.mod", "go_mod", false, ". is a literal dot, not a wildcard"},
		{"", "anything", false, "empty pattern matches nothing"},
		{"./src/*.go", "src/main.go", true, "leading ./ in the pattern is ignored"},
		{"src/*.go", "./src//main.go", true, "noise segments in the path are ignored"},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v — %s", c.pattern, c.path, got, c.why)
		}
	}
}

func TestDoubleStarSpansSegments(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
		why           string
	}{
		// ".git/**" must match ".git" itself so directory pruning works.
		{".git/**", ".git", true, "** matches zero segments"},
		{".git/**", ".git/objects/ab/cd", true, "** matches deep children"},
		{"src/**/testdata/*.json", "src/a/b/testdata/x.json", true, "** spans multiple segments"},
		{"src/**/testdata/*.json", "src/testdata/x.json", true, "** also spans zero segments"},
		{"src/**/testdata/*.json", "src/a/b/x.json", false, "literal segment after ** is still required"},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v — %s", c.pattern, c.path, got, c.why)
		}
	}
}

func TestBasenamePatternMatchesAnywhere(t *testing.T) {
	// A pattern without "/" applies to the file name at any depth —
	// this is how "*.log" excludes logs across the whole tree.
	if !Match("*.log", "deep/nested/dir/server.log") {
		t.Fatal("basename pattern should match at any depth")
	}
	if Match("*.log", "deep/nested/dir/server.go") {
		t.Fatal("basename pattern must respect the extension")
	}
}

func TestBacktrackingStarDoesNotFalseNegative(t *testing.T) {
	// Classic backtracking trap: the first '*' must be able to give
	// bytes back for the tail to match.
	if !Match("*aab", "aaab") {
		t.Fatal("*aab should match aaab")
	}
	if !Match("a*b*c", "axxbyybzzc") {
		t.Fatal("multiple stars should backtrack independently")
	}
}

func TestMatchAny(t *testing.T) {
	pats := []string{"*.log", "dist/**"}
	if !MatchAny(pats, "dist/bundle.js") {
		t.Fatal("MatchAny should hit the second pattern")
	}
	if MatchAny(pats, "src/main.go") {
		t.Fatal("MatchAny must be false when nothing matches")
	}
	if MatchAny(nil, "src/main.go") {
		t.Fatal("MatchAny over no patterns must be false")
	}
}

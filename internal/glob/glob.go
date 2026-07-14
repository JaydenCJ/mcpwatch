// Package glob implements the small glob dialect mcpwatch uses for its
// --include / --exclude flags: `*` matches within a path segment, `?`
// matches a single character, and `**` matches any number of segments
// (including zero). Character classes are deliberately not supported —
// the dialect stays predictable and shell-quoting-friendly.
package glob

import "strings"

// Match reports whether path matches pattern. Both use forward slashes.
//
// A pattern without any `/` is a basename pattern: it is matched against
// the final path segment ("*.log" excludes log files anywhere in the
// tree). Patterns containing `/` or `**` are matched against the whole
// relative path.
func Match(pattern, path string) bool {
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") && pattern != "**" {
		segs := strings.Split(path, "/")
		return matchSegment(pattern, segs[len(segs)-1])
	}
	return matchSegments(splitClean(pattern), splitClean(path))
}

// MatchAny reports whether path matches at least one of the patterns.
func MatchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if Match(p, path) {
			return true
		}
	}
	return false
}

// splitClean splits on `/`, dropping empty segments so that "./a//b"
// and "a/b" compare equal.
func splitClean(s string) []string {
	parts := strings.Split(s, "/")
	out := parts[:0]
	for _, p := range parts {
		if p != "" && p != "." {
			out = append(out, p)
		}
	}
	return out
}

// matchSegments matches a pattern segment list against a path segment
// list, giving `**` its usual "any number of segments" meaning.
func matchSegments(ps, ss []string) bool {
	if len(ps) == 0 {
		return len(ss) == 0
	}
	if ps[0] == "**" {
		// `**` may swallow zero segments…
		if matchSegments(ps[1:], ss) {
			return true
		}
		// …or one more, then try again.
		if len(ss) > 0 {
			return matchSegments(ps, ss[1:])
		}
		return false
	}
	if len(ss) == 0 {
		return false
	}
	return matchSegment(ps[0], ss[0]) && matchSegments(ps[1:], ss[1:])
}

// matchSegment matches one pattern segment (with `*` and `?`) against
// one path segment using iterative backtracking, so pathological
// patterns cannot blow the stack.
func matchSegment(pat, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(pat) && (pat[pi] == '?' || pat[pi] == s[si]):
			pi++
			si++
		case pi < len(pat) && pat[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			// Backtrack: let the last `*` absorb one more byte.
			mark++
			pi, si = star+1, mark
		default:
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}

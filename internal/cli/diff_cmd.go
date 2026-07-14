// diff_cmd.go implements `mcpwatch diff`: compare two capability dumps
// (produced by `mcpwatch dump --format json`) and exit 1 when the
// surfaces differ — a ready-made "did my refactor change the public
// surface?" gate for scripts and pre-push hooks.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/JaydenCJ/mcpwatch/internal/capability"
	"github.com/JaydenCJ/mcpwatch/internal/capdiff"
	"github.com/JaydenCJ/mcpwatch/internal/render"
)

const diffUsage = `Usage: mcpwatch diff [flags] <old.json> <new.json>

Compare two capability dumps. Pass '-' for at most one side to read it
from stdin. Exits 0 when the surfaces are identical, 1 when they
differ.

Flags:
`

func runDiff(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("diff", stderr, diffUsage)
	format := fs.String("format", "text", "output format: text or json")
	if code := parse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() != 2 {
		return usageErr(stderr, "diff: want exactly two snapshot files, got %d", fs.NArg())
	}
	if *format != "text" && *format != "json" {
		return usageErr(stderr, "diff: unknown --format %q (want text or json)", *format)
	}
	if fs.Arg(0) == "-" && fs.Arg(1) == "-" {
		return usageErr(stderr, "diff: only one side may be '-'")
	}

	oldSnap, err := readSnapshot(fs.Arg(0))
	if err != nil {
		return runtimeErr(stderr, err)
	}
	newSnap, err := readSnapshot(fs.Arg(1))
	if err != nil {
		return runtimeErr(stderr, err)
	}

	d := capdiff.Compute(oldSnap, newSnap)
	if *format == "json" {
		out, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return runtimeErr(stderr, err)
		}
		fmt.Fprintf(stdout, "%s\n", out)
	} else if d.Empty() {
		fmt.Fprintln(stdout, "no capability changes")
	} else {
		render.Diff(stdout, d)
	}
	if d.Empty() {
		return ExitOK
	}
	return ExitDiff
}

func readSnapshot(path string) (*capability.Snapshot, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
		path = "stdin"
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	snap, err := capability.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return snap, nil
}

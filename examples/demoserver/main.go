// Command demoserver is a minimal, spec-driven MCP stdio server used to
// try mcpwatch without writing a server first. Its entire capability
// surface lives in a JSON file:
//
//	go run ./examples/demoserver examples/notes-server.json
//
// Edit the JSON, and the next start serves the new surface — which is
// exactly the loop `mcpwatch run` turns into live diffs.
package main

import (
	"fmt"
	"os"

	"github.com/JaydenCJ/mcpwatch/internal/demosrv"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: demoserver <spec.json>")
		os.Exit(2)
	}
	spec, err := demosrv.LoadSpec(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "demoserver: %v\n", err)
		os.Exit(1)
	}
	if err := demosrv.Serve(spec, os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(1)
	}
}

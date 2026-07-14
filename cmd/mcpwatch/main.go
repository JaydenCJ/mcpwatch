// Command mcpwatch is the dev-loop runner for stdio MCP servers:
// restart on file changes, re-dump capabilities, print the diff.
package main

import (
	"os"

	"github.com/JaydenCJ/mcpwatch/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}

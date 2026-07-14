// mcpwatch — dev-loop runner that restarts a stdio MCP server on file
// changes and live-diffs its capability surface after every reload.
//
// version:    0.1.0
// author:     JaydenCJ
// license:    MIT
// repository: https://github.com/JaydenCJ/mcpwatch
// keywords:   mcp, model-context-protocol, hot-reload, dev-loop, stdio, json-rpc, developer-tools
//
// Zero runtime dependencies: the require list below is intentionally empty
// and must stay that way (see CONTRIBUTING.md, "Ground rules").
module github.com/JaydenCJ/mcpwatch

go 1.22

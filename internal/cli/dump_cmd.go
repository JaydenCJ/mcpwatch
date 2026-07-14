// dump_cmd.go implements `mcpwatch dump`: start the server once,
// perform the MCP handshake, print the capability surface, exit. The
// same start-and-dump primitive is reused by the run loop after every
// restart.
package cli

import (
	"fmt"
	"io"

	"github.com/JaydenCJ/mcpwatch/internal/capability"
	"github.com/JaydenCJ/mcpwatch/internal/mcpclient"
	"github.com/JaydenCJ/mcpwatch/internal/render"
	"github.com/JaydenCJ/mcpwatch/internal/runner"
)

const dumpUsage = `Usage: mcpwatch dump [flags] -- <server command…>

Start the server once, print its capability surface (text or JSON), and
shut it down.

Flags:
`

func runDump(args []string, stdout, stderr io.Writer) int {
	flagArgs, server := splitServerCommand(args)
	fs := newFlagSet("dump", stderr, dumpUsage)
	var common commonServerFlags
	common.register(fs)
	format := fs.String("format", "text", "output format: text or json")
	if code := parse(fs, flagArgs); code >= 0 {
		return code
	}
	if fs.NArg() > 0 {
		return usageErr(stderr, "dump: unexpected argument %q (server command goes after --)", fs.Arg(0))
	}
	if len(server) == 0 {
		return usageErr(stderr, "dump: no server command; usage: mcpwatch dump [flags] -- <command…>")
	}
	if *format != "text" && *format != "json" {
		return usageErr(stderr, "dump: unknown --format %q (want text or json)", *format)
	}

	snap, err := startAndDump(server, &common, stderr)
	if err != nil {
		return runtimeErr(stderr, err)
	}
	if *format == "json" {
		out, err := capability.Encode(snap)
		if err != nil {
			return runtimeErr(stderr, err)
		}
		_, _ = stdout.Write(out)
		return ExitOK
	}
	render.Snapshot(stdout, snap)
	return ExitOK
}

// startAndDump spawns the server, dumps its capability snapshot, and
// stops it again. On failure the server is still torn down and its
// exit state is folded into the error, because "initialize timed out"
// alone is useless when the real story is "exit code 1: SyntaxError".
func startAndDump(server []string, common *commonServerFlags, stderr io.Writer) (*capability.Snapshot, error) {
	errSink := io.Writer(io.Discard)
	if !common.quietServer {
		errSink = runner.PrefixWriter(stderr, "[server] ")
	}
	proc, err := runner.Start(server, "", errSink)
	if err != nil {
		return nil, err
	}
	client := mcpclient.New(proc.Stdout, proc.Stdin)
	client.Timeout = common.timeout
	snap, derr := client.DumpSnapshot(common.proto)
	proc.Stop(common.killTimeout)
	client.Close()
	if derr != nil {
		if desc := proc.ExitDescription(); desc != "exited cleanly" {
			return nil, fmt.Errorf("%w (server %s)", derr, desc)
		}
		return nil, derr
	}
	return snap, nil
}

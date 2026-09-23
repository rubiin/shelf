package main

import (
	"errors"
	"os"

	"shelf/internal/cli"
	"shelf/internal/tui"
)

// version is stamped by release builds with -X main.version.
var version = "dev"

func main() {
	os.Exit(exitCode(run()))
}

// run executes the CLI; cli.Execute has already printed any error.
func run() error {
	cli.Version = version
	return cli.Execute(os.Args[1:], os.Stdout, os.Stderr)
}

// exitCode maps a run result to the process exit status: 0 on success, 130 for
// an interactive cancellation, 2 for any other failure.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, tui.ErrCancelled):
		return 130
	default:
		return 2
	}
}

package main

import (
	"os"

	"shelf/internal/cli"
)

// version is stamped by release builds with -X main.version.
var version = "dev"

func main() {
	cli.Version = version
	// Execute already printed the error; exit 2 signals failure.
	if err := cli.Execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		os.Exit(2)
	}
}

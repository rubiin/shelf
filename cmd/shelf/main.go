package main

import (
	"os"

	"shelf/internal/cli"
)

// version is the release version, which builds stamp with -X main.version.
var version = "dev"

func main() {
	cli.Version = version
	// A failed command reports status 2 after the error is printed.
	if err := cli.Execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		os.Exit(2)
	}
}

package main

import (
	"os"

	"shelf/internal/cli"
)

func main() {
	// A failed command reports status 2 after the error is printed.
	if err := cli.Execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		os.Exit(2)
	}
}

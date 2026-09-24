package main

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"shelf/internal/tui"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, 0},
		{"failure", errors.New("boom"), 2},
		{"interactive cancel", tui.ErrCancelled, 130},
		{"wrapped cancellation", fmt.Errorf("select: %w", tui.ErrCancelled), 130},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCode(tt.err); got != tt.want {
				t.Fatalf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestRunExecutesTheVersionFlag(t *testing.T) {
	os.Args = []string{"shelf", "--version"}
	if err := run(); err != nil {
		t.Fatalf("run with --version: %v", err)
	}
}

// TestMainDelegatesToOSExit pins the entrypoint wiring: main runs the CLI and
// passes the mapped exit status to os.Exit (redirected through osExit so the
// test process survives).
func TestMainDelegatesToOSExit(t *testing.T) {
	original := osExit
	osExit = func(code int) {
		if code != 0 {
			t.Errorf("main exited with %d, want 0 for --version", code)
		}
	}
	t.Cleanup(func() { osExit = original })
	os.Args = []string{"shelf", "--version"}
	main()
}

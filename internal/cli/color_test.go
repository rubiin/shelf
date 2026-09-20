package cli

import "testing"

func TestColorEnabled(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		tty      bool
		noColor  string
		term     string
		expected bool
	}{
		{"always enables color", "always", false, "", "xterm-256color", true},
		{"never disables color", "never", true, "", "xterm-256color", false},
		{"auto enables color on a terminal", "auto", true, "", "xterm-256color", true},
		{"auto disables color on a pipe", "auto", false, "", "xterm-256color", false},
		{"auto honors NO_COLOR", "auto", true, "1", "xterm-256color", false},
		{"auto honors dumb terminals", "auto", true, "", "dumb", false},
		{"empty mode falls back to auto", "", true, "", "xterm-256color", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", test.noColor)
			t.Setenv("TERM", test.term)
			if got := colorEnabled(test.mode, test.tty); got != test.expected {
				t.Errorf("colorEnabled(%q, %v) = %v, want %v", test.mode, test.tty, got, test.expected)
			}
		})
	}
}

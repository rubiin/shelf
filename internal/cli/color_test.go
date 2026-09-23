package cli

import (
	"bytes"
	"strings"
	"testing"
)

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

func TestLineWriterWriteEndsWithReset(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// A chunk ending in a newline must still end with the reset, so the last
		// styled line does not leave the terminal colored.
		{
			name: "chunk ends in newline",
			in:   "line one\n",
			want: ansiStatusColor + "line one" + ansiReset + "\n" + ansiStatusColor + ansiReset,
		},
		{
			name: "chunk ends in newline after several lines",
			in:   "one\ntwo\n",
			want: ansiStatusColor + "one" + ansiReset + "\n" + ansiStatusColor + "two" + ansiReset + "\n" + ansiStatusColor + ansiReset,
		},
		// A mid-chunk newline resets before the newline and reopens the style after it.
		{
			name: "mid-chunk newline",
			in:   "line one\nline two",
			want: ansiStatusColor + "line one" + ansiReset + "\n" + ansiStatusColor + "line two" + ansiReset,
		},
		{
			name: "no trailing newline",
			in:   "plain",
			want: ansiStatusColor + "plain" + ansiReset,
		},
		{
			name: "empty chunk writes nothing",
			in:   "",
			want: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			writer := lineWriter{dst: &buffer, style: ansiStatusColor, on: true}
			written, err := writer.Write([]byte(test.in))
			if err != nil {
				t.Fatal(err)
			}
			if written != len(test.in) {
				t.Fatalf("Write returned %d, want %d", written, len(test.in))
			}
			got := buffer.String()
			if got != test.want {
				t.Fatalf("output = %q, want %q", got, test.want)
			}
			if test.in != "" && !strings.HasSuffix(got, ansiReset) {
				t.Fatalf("output %q does not end with the reset escape", got)
			}
		})
	}
}

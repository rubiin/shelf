package cli

import (
	"bytes"
	"errors"
	"os"
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

func TestIsTerminalWriters(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Fatal("a plain buffer is not a terminal")
	}
	file, err := os.CreateTemp(t.TempDir(), "term")
	if err != nil {
		t.Fatal(err)
	}
	// A real file always passes Stat, so the mode check runs either way.
	_ = isTerminal(file)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if isTerminal(file) {
		t.Fatal("a closed file should fail Stat and report no terminal")
	}
}

// errWriter fails every write, exposing callers' write-error handling.
type errWriter struct{}

func (errWriter) Write(_ []byte) (int, error) { return 0, errors.New("write failed") }

func TestColorsWarningErrorAndWarn(t *testing.T) {
	enabled := colors{enabled: true}
	// warning pads the prefix to the status width regardless of color mode.
	if got := enabled.warning("danger"); !strings.HasSuffix(got, ansiWarningColor+"    danger"+ansiReset) {
		t.Fatalf("colored warning = %q", got)
	}
	if got := (colors{enabled: false}).warning("danger"); got != "    danger" {
		t.Fatalf("plain warning = %q, want padded plain prefix", got)
	}
	if got := enabled.error("bad"); got != ansiErrorColor+"bad"+ansiReset {
		t.Fatalf("colored error = %q", got)
	}
	if got := (colors{enabled: false}).error("bad"); got != "bad" {
		t.Fatalf("plain error = %q", got)
	}
	if got := enabled.warn("note"); got != ansiWarningColor+"note"+ansiReset {
		t.Fatalf("colored warn = %q", got)
	}
	if got := (colors{enabled: false}).warn("note"); got != "note" {
		t.Fatalf("plain warn = %q", got)
	}
}

func TestStyledLinesNilAndWrite(t *testing.T) {
	if writer := styledLines(nil, ansiStatusColor); writer != nil {
		t.Fatalf("styledLines(nil) = %v, want nil", writer)
	}

	originalColor := color
	t.Cleanup(func() { color = originalColor })

	var buffer bytes.Buffer
	color = "always"
	colorWriter := styledLines(&buffer, ansiStatusColor)
	if _, err := colorWriter.Write([]byte("warm")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buffer.String(), ansiStatusColor+"warm"+ansiReset) {
		t.Fatalf("styled write = %q, want the reset-terminated style", buffer.String())
	}

	buffer.Reset()
	color = "never"
	plainWriter := styledLines(&buffer, ansiStatusColor)
	if _, err := plainWriter.Write([]byte("cool")); err != nil {
		t.Fatal(err)
	}
	if buffer.String() != "cool" {
		t.Fatalf("plain styled write = %q", buffer.String())
	}

	buffer.Reset()
	if _, err := (lineWriter{dst: &buffer, on: false}).Write([]byte("plain")); err != nil {
		t.Fatal(err)
	}
	if buffer.String() != "plain" {
		t.Fatalf("unstyled line writer output = %q", buffer.String())
	}

	if _, err := (lineWriter{dst: errWriter{}, style: "", on: true}).Write([]byte("boom")); err == nil {
		t.Fatal("line writer swallowed a destination write error")
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

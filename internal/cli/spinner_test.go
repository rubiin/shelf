package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/yarlson/pin"
)

// The spinner must stay out of the way whenever diagnostics is not an
// interactive terminal, which is what every test and shell pipeline sees.
func TestProgressSpinnerIsSilentWithoutATerminal(t *testing.T) {
	originalQuiet, originalVerbose := quiet, verbose
	t.Cleanup(func() { quiet, verbose = originalQuiet, originalVerbose })

	tests := []struct {
		name        string
		diagnostics *bytes.Buffer
		quiet       bool
		verbose     bool
	}{
		{name: "buffer output", diagnostics: &bytes.Buffer{}},
		{name: "quiet", diagnostics: &bytes.Buffer{}, quiet: true},
		{name: "verbose", diagnostics: &bytes.Buffer{}, verbose: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quiet, verbose = test.quiet, test.verbose
			stop := startProgressSpinner(test.diagnostics, "Installing plugins")
			stop()
			stop() // stopping twice must be safe
			if test.diagnostics.Len() != 0 {
				t.Fatalf("spinner wrote to a non-terminal: %q", test.diagnostics)
			}
		})
	}
}

func TestProgressSpinnerIgnoresNilDiagnostics(t *testing.T) {
	if spinnerEnabled(nil) {
		t.Fatal("spinner enabled for a nil writer")
	}
	startProgressSpinner(nil, "Installing plugins")()
}

// TestProgressSpinnerAnimatesAndClears drives pin on a forced terminal, since
// the gate above keeps it from running under a non-terminal writer.
func TestProgressSpinnerAnimatesAndClears(t *testing.T) {
	pin.SetForceInteractive(true)
	t.Cleanup(func() { pin.SetForceInteractive(false) })

	var output bytes.Buffer
	_, stop := newProgressSpinner(&output, "Installing plugins")
	time.Sleep(150 * time.Millisecond)
	stop()
	stop()

	text := output.String()
	if !strings.Contains(text, "Installing plugins") {
		t.Fatalf("spinner never wrote a frame: %q", text)
	}
	if !strings.HasSuffix(text, "\r\033[K") {
		t.Fatalf("spinner did not clear its line: %q", text)
	}
}

func TestProgressSpinnerColorsWhenEnabled(t *testing.T) {
	pin.SetForceInteractive(true)
	t.Cleanup(func() { pin.SetForceInteractive(false) })
	original := color
	t.Cleanup(func() { color = original })

	color = "always"
	var output bytes.Buffer
	_, stop := newProgressSpinner(&output, "Installing plugins")
	time.Sleep(150 * time.Millisecond)
	stop()
	if !strings.Contains(output.String(), pin.ColorCyan.String()) {
		t.Fatalf("colored spinner emitted no color: %q", output.String())
	}

	color = "never"
	output.Reset()
	_, stop = newProgressSpinner(&output, "Installing plugins")
	time.Sleep(150 * time.Millisecond)
	stop()
	if strings.Contains(output.String(), pin.ColorCyan.String()) {
		t.Fatalf("colorless spinner emitted color: %q", output.String())
	}
}

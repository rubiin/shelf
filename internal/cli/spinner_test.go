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
			shown := startProgressSpinner(test.diagnostics, "Installing plugins")
			shown.Stop()
			shown.Stop() // stopping twice must be safe
			if test.diagnostics.Len() != 0 {
				t.Fatalf("spinner wrote to a non-terminal: %q", test.diagnostics)
			}
		})
	}
}

// Without an animation the writer must be a pass-through, so a pipeline never
// sees the clear-line escape.
func TestProgressWriteWithoutASpinnerIsPlain(t *testing.T) {
	var output bytes.Buffer
	shown := newProgress(&output)
	if _, err := shown.Write([]byte("Selected shelf 1.0.0\n")); err != nil {
		t.Fatalf("write through the spinner: %v", err)
	}
	if output.String() != "Selected shelf 1.0.0\n" {
		t.Fatalf("output = %q, want the text unchanged", output.String())
	}
}

func TestProgressSpinnerIgnoresNilDiagnostics(t *testing.T) {
	if spinnerEnabled(nil) {
		t.Fatal("spinner enabled for a nil writer")
	}
	shown := newProgress(nil)
	if shown.Writer() != nil {
		t.Fatal("a nil diagnostics writer produced a writer")
	}
	if _, err := shown.Write([]byte("Ignored\n")); err != nil {
		t.Fatalf("write to a disabled spinner: %v", err)
	}
	shown.Stop()
}

// The rest of these drive pin on a forced terminal, since the gate above keeps
// it from running under a non-terminal writer.

// animatingProgress starts a progress over a buffer and lets a test's Show
// calls through the terminal gate.
func animatingProgress(t *testing.T, output *bytes.Buffer, message string) *progress {
	t.Helper()
	pin.SetForceInteractive(true)
	t.Cleanup(func() { pin.SetForceInteractive(false) })
	shown := newProgress(output)
	shown.animate(message)
	return shown
}

func TestProgressSpinnerAnimatesAndClears(t *testing.T) {
	var output bytes.Buffer
	shown := animatingProgress(t, &output, "Installing plugins")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()
	shown.Stop()

	text := output.String()
	if !strings.Contains(text, "Installing plugins") {
		t.Fatalf("spinner never wrote a frame: %q", text)
	}
	if !strings.HasSuffix(text, "\r\033[K") {
		t.Fatalf("spinner did not clear its line: %q", text)
	}
}

func TestProgressSpinnerColorsWhenEnabled(t *testing.T) {
	original := color
	t.Cleanup(func() { color = original })

	color = "always"
	var output bytes.Buffer
	shown := animatingProgress(t, &output, "Installing plugins")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()
	if !strings.Contains(output.String(), pin.ColorCyan.String()) {
		t.Fatalf("colored spinner emitted no color: %q", output.String())
	}

	color = "never"
	output.Reset()
	shown = animatingProgress(t, &output, "Installing plugins")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()
	if strings.Contains(output.String(), pin.ColorCyan.String()) {
		t.Fatalf("colorless spinner emitted color: %q", output.String())
	}
}

// A frame carries no newline, so output written alongside the spinner has to
// clear the line first or it merges with the frame.
func TestProgressWriteClearsTheSpinnerLine(t *testing.T) {
	var output bytes.Buffer
	shown := animatingProgress(t, &output, "Installing plugins")
	if _, err := shown.Write([]byte("Selected shelf 1.0.0\n")); err != nil {
		t.Fatalf("write through the spinner: %v", err)
	}
	shown.Stop()

	if !strings.Contains(output.String(), "\r\033[KSelected shelf 1.0.0\n") {
		t.Fatalf("output landed on the spinner line: %q", output.String())
	}
}

// Output without a trailing newline would be overwritten by the next frame.
func TestProgressWriteClosesAnUnterminatedLine(t *testing.T) {
	var output bytes.Buffer
	shown := animatingProgress(t, &output, "Installing plugins")
	if _, err := shown.Write([]byte("partial")); err != nil {
		t.Fatalf("write through the spinner: %v", err)
	}
	shown.Stop()

	if !strings.Contains(output.String(), "partial\n") {
		t.Fatalf("unterminated output was left on the spinner line: %q", output.String())
	}
}

// A phase is shown only when it is announced, so nothing animates before the
// work behind it starts.
func TestProgressDoesNotAnimateBeforeShow(t *testing.T) {
	pin.SetForceInteractive(true)
	t.Cleanup(func() { pin.SetForceInteractive(false) })

	var output bytes.Buffer
	shown := newProgress(&output)
	time.Sleep(150 * time.Millisecond)
	if output.Len() != 0 {
		t.Fatalf("progress animated before any phase: %q", output.String())
	}
	shown.animate("Downloading shelf")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()
	if !strings.Contains(output.String(), "Downloading shelf") {
		t.Fatalf("announced phase never animated: %q", output.String())
	}
}

// Hide stops the animation; a later phase brings it back.
func TestProgressHideAndShowAgain(t *testing.T) {
	var output bytes.Buffer
	shown := animatingProgress(t, &output, "Downloading shelf")
	time.Sleep(150 * time.Millisecond)
	shown.Hide()
	if !strings.HasSuffix(output.String(), "\r\033[K") {
		t.Fatalf("hide left the line dirty: %q", output.String())
	}

	shown.animate("Verifying shelf")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()
	if !strings.Contains(output.String(), "Verifying shelf") {
		t.Fatalf("the second phase never animated: %q", output.String())
	}
}

// A stopped progress must not come back to life when a later phase starts.
func TestProgressShowAfterStopStaysStopped(t *testing.T) {
	var output bytes.Buffer
	shown := animatingProgress(t, &output, "Downloading shelf")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()
	shown.animate("Verifying shelf")
	before := output.Len()
	time.Sleep(150 * time.Millisecond)
	if output.Len() != before {
		t.Fatalf("progress animated after Stop: %q", output.String())
	}
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"shelf/internal/selfupdate"
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

// On a terminal a phase is animated instead of logged, so it is not written
// twice.
func TestSelfUpdateAnimatesPhasesOnATerminal(t *testing.T) {
	forceSpinner.Store(true)
	t.Cleanup(func() { forceSpinner.Store(false) })

	original := runUpdate
	runUpdate = func(_ context.Context, options selfupdate.Options) (selfupdate.Result, error) {
		if options.Progress == nil {
			t.Error("self-update got no progress hook on a terminal")
		} else {
			options.Progress("Downloading shelf")
		}
		if options.Diagnostics == nil {
			t.Error("self-update ran without diagnostics")
		}
		return selfupdate.Result{Updated: false, Next: "9.9.9"}, nil
	}
	t.Cleanup(func() { runUpdate = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"self-update"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
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
func animatingProgress(t *testing.T, output io.Writer, message string) *progress {
	t.Helper()
	forceSpinner.Store(true)
	t.Cleanup(func() { forceSpinner.Store(false) })
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
	// The frame is cleared and its line ended, so later output starts fresh.
	if !strings.HasSuffix(text, "\r\033[K\n") {
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
	if !strings.Contains(output.String(), ansiSpinnerColor) {
		t.Fatalf("colored spinner emitted no color: %q", output.String())
	}

	color = "never"
	output.Reset()
	shown = animatingProgress(t, &output, "Installing plugins")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()
	if strings.Contains(output.String(), ansiSpinnerColor) {
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
	forceSpinner.Store(true)
	t.Cleanup(func() { forceSpinner.Store(false) })

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

// Show is the gated entry point a caller uses, so it must start the animation
// on a terminal and stay silent off one.
func TestProgressShowStartsOnATerminalOnly(t *testing.T) {
	tests := []struct {
		name    string
		quiet   bool
		verbose bool
		want    bool
	}{
		{name: "buffer output"},
		{name: "quiet", quiet: true},
		{name: "verbose", verbose: true},
		{name: "terminal", want: true},
	}
	originalQuiet, originalVerbose := quiet, verbose
	t.Cleanup(func() { quiet, verbose = originalQuiet, originalVerbose })

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quiet, verbose = test.quiet, test.verbose
			var output bytes.Buffer
			forceSpinner.Store(test.want)
			t.Cleanup(func() { forceSpinner.Store(false) })

			shown := newProgress(&output)
			shown.Show("Downloading shelf")
			if shown.running != test.want {
				t.Fatalf("running = %t, want %t", shown.running, test.want)
			}
			shown.Stop()
		})
	}
}

// Stopping must end the spinner's line, so what prints next cannot continue
// the frame's line.
func TestProgressStopEndsTheSpinnerLine(t *testing.T) {
	var output bytes.Buffer
	shown := animatingProgress(t, &output, "Downloading shelf")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()
	if !strings.HasSuffix(output.String(), "\r\033[K\n") {
		t.Fatalf("stop left the line dirty: %q", output.String())
	}
}

// A second phase while the first is still running relabels the frame instead
// of restarting it.
func TestProgressShowRelabelsARunningSpinner(t *testing.T) {
	var output bytes.Buffer
	shown := animatingProgress(t, &output, "Downloading shelf")
	time.Sleep(150 * time.Millisecond)
	shown.animate("Verifying shelf")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()

	text := output.String()
	if !strings.Contains(text, "Verifying shelf") {
		t.Fatalf("output = %q, want the relabeled frame", text)
	}
	// UpdateMessage repaints in place, so the old label never returns after the
	// relabel; a restarted spinner would redraw it.
	relabel := strings.LastIndex(text, "Verifying shelf")
	if trailing := text[relabel:]; strings.Contains(trailing, "Downloading shelf") {
		t.Fatalf("the old phase came back after the relabel: %q", trailing)
	}
}

// Hide stops the animation; a later phase brings it back.
func TestProgressHideAndShowAgain(t *testing.T) {
	var output bytes.Buffer
	shown := animatingProgress(t, &output, "Downloading shelf")
	time.Sleep(150 * time.Millisecond)
	shown.Hide()
	if !strings.HasSuffix(output.String(), "\r\033[K\n") {
		t.Fatalf("hide left the line dirty: %q", output.String())
	}

	shown.animate("Verifying shelf")
	time.Sleep(150 * time.Millisecond)
	shown.Stop()
	if !strings.Contains(output.String(), "Verifying shelf") {
		t.Fatalf("the second phase never animated: %q", output.String())
	}
}

// A write failure must surface instead of being swallowed by the animation,
// whether it hits the text itself or the newline that closes an unterminated
// line.
func TestProgressWritePropagatesAnError(t *testing.T) {
	// The first write is the opening frame, so the sequence under test starts
	// at the second.
	tests := []struct {
		name   string
		failAt int
		data   string
	}{
		{name: "clear line", failAt: 2, data: "Selected shelf 1.0.0\n"},
		{name: "text", failAt: 3, data: "Selected shelf 1.0.0\n"},
		{name: "closing newline", failAt: 4, data: "partial"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shown := animatingProgress(t, newFailingWriter(test.failAt), "Downloading shelf")
			if _, err := shown.Write([]byte(test.data)); err == nil {
				t.Fatal("write through a failing diagnostics writer succeeded")
			}
			shown.Stop()
		})
	}
}

// failingWriter succeeds until the nth write, so a test can fail one specific
// step of the clear-line-then-write-then-close sequence.
type failingWriter struct {
	mu     sync.Mutex
	writes int
	failAt int
}

var errFailed = errors.New("diagnostics write failed")

func newFailingWriter(failAt int) *failingWriter { return &failingWriter{failAt: failAt} }

func (w *failingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	if w.writes == w.failAt {
		return 0, errFailed
	}
	return len(data), nil
}

func (w *failingWriter) terminal() io.Writer { return nil }

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

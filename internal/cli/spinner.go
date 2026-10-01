package cli

import (
	"context"
	"io"
	"sync"

	"github.com/yarlson/pin"
)

// spinnerEnabled reports whether a progress spinner should animate on writer:
// only an interactive terminal, and never under --quiet or --verbose, so shell
// pipelines and raw diagnostics stay intact.
func spinnerEnabled(writer io.Writer) bool {
	return !quiet && !verbose && writer != nil && isTerminal(writer)
}

// startProgressSpinner shows progress for a network-heavy phase and returns a
// function that clears the spinner line; it is a no-op when the spinner is
// disabled.
func startProgressSpinner(diagnostics io.Writer, message string) func() {
	if !spinnerEnabled(diagnostics) {
		return func() {}
	}
	_, stop := newProgressSpinner(diagnostics, message)
	return stop
}

// newProgressSpinner builds a pin spinner writing to diagnostics and starts it.
// The returned stop function clears the line and is safe to call repeatedly.
func newProgressSpinner(diagnostics io.Writer, message string) (*pin.Pin, func()) {
	options := []pin.Option{pin.WithWriter(diagnostics)}
	if colorEnabled(color, true) {
		options = append(options, pin.WithSpinnerColor(pin.ColorCyan))
	}
	spinner := pin.New(message, options...)
	cancel := spinner.Start(context.Background())
	var once sync.Once
	stop := func() {
		once.Do(func() {
			spinner.Stop()
			cancel()
		})
	}
	return spinner, stop
}

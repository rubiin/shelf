package cli

import (
	"context"
	"fmt"
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

// progress is an optional animation plus the writer other output must go
// through. Frames carry no newline, so every write clears the line first;
// otherwise a log line merges with the frame instead of following it.
type progress struct {
	diagnostics io.Writer
	message     string
	spinner     *pin.Pin
	mu          sync.Mutex
	running     bool
	stopped     bool
}

// startProgressSpinner shows progress for a network-heavy phase.
func startProgressSpinner(diagnostics io.Writer, message string) *progress {
	shown := newProgress(diagnostics)
	shown.Show(message)
	return shown
}

// newProgress wraps diagnostics without animating, for a caller that should
// show a phase only once the work behind it has started.
func newProgress(diagnostics io.Writer) *progress {
	return &progress{diagnostics: diagnostics}
}

// Show animates a phase, or relabels the one already running. It is ignored
// once the progress is stopped.
func (p *progress) Show(message string) {
	if !spinnerEnabled(p.diagnostics) {
		return
	}
	p.animate(message)
}

// animate starts or relabels the animation, bypassing the terminal gate.
func (p *progress) animate(message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.message = message
	if p.stopped {
		return
	}
	if p.running {
		p.spinner.UpdateMessage(message)
		return
	}
	options := []pin.Option{pin.WithWriter(p.diagnostics)}
	if colorEnabled(color, true) {
		options = append(options, pin.WithSpinnerColor(pin.ColorCyan))
	}
	p.spinner = pin.New(message, options...)
	p.running = true
	p.spinner.Start(context.Background())
}

// Hide clears the animation; a later Show brings it back.
func (p *progress) Hide() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.halt()
}

// Stop clears the line for good; it is safe to call more than once.
func (p *progress) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	p.halt()
}

// halt stops the animation; the caller holds mu.
func (p *progress) halt() {
	if p.spinner == nil || !p.running {
		return
	}
	p.running = false
	p.spinner.Stop()
}

// terminal reports the writer behind the spinner, so styling still recognizes
// it as a terminal through the progress wrapper.
func (p *progress) terminal() io.Writer {
	return p.diagnostics
}

// Writer returns diagnostics for everything but the spinner itself, or nil
// when there are none.
func (p *progress) Writer() io.Writer {
	if p.diagnostics == nil {
		return nil
	}
	return p
}

// Write clears the spinner's line, emits the text, and leaves the line ready
// for the next frame. Without an animation there is no line to clear.
func (p *progress) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.diagnostics == nil {
		return len(data), nil
	}
	if p.spinner == nil {
		return p.diagnostics.Write(data)
	}
	if _, err := fmt.Fprint(p.diagnostics, "\r\033[K"); err != nil {
		return 0, err
	}
	written, err := p.diagnostics.Write(data)
	if err != nil {
		return written, err
	}
	// An unterminated line would be overwritten by the next frame, so close it.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if _, err := fmt.Fprint(p.diagnostics, "\n"); err != nil {
			return written, err
		}
	}
	return written, nil
}

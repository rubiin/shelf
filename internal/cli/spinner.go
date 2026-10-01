package cli

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// spinnerInterval is how often a new frame replaces the last; fast enough
	// to read as motion, slow enough not to flood a terminal over ssh.
	spinnerInterval = 100 * time.Millisecond
	// ansiEraseLine returns to column one and erases to the end, which is how
	// a frame is replaced or removed without disturbing earlier lines.
	ansiEraseLine = "\r\033[K"
	// ansiSpinnerColor is the glyph's color; the message stays in the default
	// so it reads as terminal output rather than decoration.
	ansiSpinnerColor = "\x1b[36m"
)

// spinnerFrames cycle a single dot around the glyph, so the line keeps one
// width and only the dot moves.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// forceSpinner drives the animation without a terminal, so a test can watch the
// frames a real terminal would draw.
var forceSpinner atomic.Bool

// spinnerEnabled reports whether a progress spinner should animate on writer:
// only an interactive terminal, and never under --quiet or --verbose, so shell
// pipelines and raw diagnostics stay intact.
func spinnerEnabled(writer io.Writer) bool {
	return !quiet && !verbose && writer != nil && (forceSpinner.Load() || isTerminal(writer))
}

// progress is an optional animation plus the writer other output must go
// through. Frames carry no newline, so every write clears the line first;
// otherwise a log line merges with the frame instead of following it.
type progress struct {
	diagnostics io.Writer
	message     string
	frame       int
	coloured    bool
	mu          sync.Mutex
	stop        chan struct{}
	done        chan struct{}
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
		// Repaint in place, so the phase changes without a new line.
		p.frame = (p.frame + 1) % len(spinnerFrames)
		p.paint()
		return
	}
	p.frame = 0
	p.coloured = colorEnabled(color, true)
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	p.running = true
	go p.animateLoop(p.stop, p.done)
	p.paint()
}

// animateLoop repaints until stop closes, then reports through done. The
// channels are arguments so a relabelled or restarted animation cannot leave a
// loop watching a channel it no longer owns.
func (p *progress) animateLoop(stop, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			p.frame = (p.frame + 1) % len(spinnerFrames)
			p.paint()
			p.mu.Unlock()
		}
	}
}

// paint draws the current frame in place; the caller holds mu.
func (p *progress) paint() {
	if p.diagnostics == nil {
		return
	}
	glyph := spinnerFrames[p.frame]
	if p.coloured {
		glyph = ansiSpinnerColor + glyph + ansiReset
	}
	_, _ = fmt.Fprint(p.diagnostics, ansiEraseLine, glyph, " ", p.message)
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

// halt stops the animation and ends its line, so whatever prints next starts
// fresh instead of continuing the frame; the caller holds mu.
func (p *progress) halt() {
	if !p.running {
		return
	}
	p.running = false
	close(p.stop)
	// Wait for the loop so it cannot paint over the erase below.
	<-p.done
	if _, err := fmt.Fprint(p.diagnostics, ansiEraseLine, "\n"); err != nil {
		return
	}
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
	if !p.running {
		return p.diagnostics.Write(data)
	}
	if _, err := fmt.Fprint(p.diagnostics, ansiEraseLine); err != nil {
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

package cli

import (
	"fmt"
	"io"
)

// logger writes diagnostics: verbatim headers and right-aligned statuses.
type logger struct {
	diagnostics io.Writer
	colors      colors
	quiet       bool
	verbose     bool
}

func newLogger(diagnostics io.Writer) logger {
	return logger{diagnostics: diagnostics, colors: newColors(color, diagnostics), quiet: quiet, verbose: verbose}
}

// header prints a title such as Loaded or Locked, which is never aligned.
func (l logger) header(prefix, message string) {
	if l.quiet {
		return
	}
	_, _ = fmt.Fprintf(l.diagnostics, "%s %s\n", l.colors.header(prefix), message)
}

// status prints a right-aligned action such as Checked.
func (l logger) status(prefix, message string) {
	if l.quiet {
		return
	}
	_, _ = fmt.Fprintf(l.diagnostics, "%s %s\n", l.colors.status(prefix), message)
}

func (l logger) verboseHeader(prefix, message string) {
	if l.quiet || !l.verbose {
		return
	}
	l.header(prefix, message)
}

func (l logger) verboseStatus(prefix, message string) {
	if l.quiet || !l.verbose {
		return
	}
	l.status(prefix, message)
}

// verboseWarning prints a yellow status, used for cleanup.
func (l logger) verboseWarning(prefix, message string) {
	if l.quiet || !l.verbose {
		return
	}
	_, _ = fmt.Fprintf(l.diagnostics, "%s %s\n", l.colors.warning(prefix), message)
}

// dim styles a message fragment as a secondary detail, such as a path.
func (l logger) dim(text string) string { return l.colors.dim(text) }

func (l logger) warning(prefix, message string) {
	if l.quiet {
		return
	}
	_, _ = fmt.Fprintf(l.diagnostics, "%s %s\n", l.colors.warning(prefix), message)
}

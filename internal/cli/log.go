package cli

import (
	"fmt"
	"io"
)

// logger writes headers verbatim and statuses right-aligned to the diagnostics writer.
type logger struct {
	diagnostics io.Writer
	colors      colors
	quiet       bool
	verbose     bool
}

func newLogger(diagnostics io.Writer) logger {
	return logger{diagnostics: diagnostics, colors: newColors(color, diagnostics), quiet: quiet, verbose: verbose}
}

// header prints an unaligned title such as Locked.
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

// verboseWarning is a yellow status, used by cleanup.
func (l logger) verboseWarning(prefix, message string) {
	if l.quiet || !l.verbose {
		return
	}
	_, _ = fmt.Fprintf(l.diagnostics, "%s %s\n", l.colors.warning(prefix), message)
}

func (l logger) dim(text string) string { return l.colors.dim(text) }

func (l logger) warning(prefix, message string) {
	if l.quiet {
		return
	}
	_, _ = fmt.Fprintf(l.diagnostics, "%s %s\n", l.colors.warning(prefix), message)
}

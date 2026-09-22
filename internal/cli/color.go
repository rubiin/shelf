package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	ansiReset        = "\x1b[0m"
	ansiHeaderColor  = "\x1b[1;35m"
	ansiStatusColor  = "\x1b[1;36m"
	ansiWarningColor = "\x1b[1;33m"
	ansiErrorColor   = "\x1b[1;31m"
	ansiSuccessColor = "\x1b[1;32m"
	ansiDimColor     = "\x1b[2m"
	statusWidth      = 10
)

// colorEnabled reports whether ANSI colors should be emitted for the requested mode.
func colorEnabled(mode string, tty bool) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	default:
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		if os.Getenv("TERM") == "dumb" {
			return false
		}
		return tty
	}
}

// isTerminal reports whether w is a character device such as a TTY.
func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// colors formats diagnosis prefixes: bold magenta headers, bold cyan statuses, bold yellow warnings.
type colors struct{ enabled bool }

func newColors(mode string, diagnostics io.Writer) colors {
	return colors{enabled: colorEnabled(mode, isTerminal(diagnostics))}
}

// writerColors builds colors for a writer under the current --color mode, honoring NO_COLOR and dumb terminals.
func writerColors(w io.Writer) colors {
	return colors{enabled: colorEnabled(color, isTerminal(w))}
}

func (c colors) header(prefix string) string {
	if !c.enabled {
		return prefix
	}
	return ansiHeaderColor + prefix + ansiReset
}

func (c colors) status(prefix string) string {
	prefix = fmt.Sprintf("%*s", statusWidth, prefix)
	if !c.enabled {
		return prefix
	}
	return ansiStatusColor + prefix + ansiReset
}

func (c colors) warning(prefix string) string {
	prefix = fmt.Sprintf("%*s", statusWidth, prefix)
	if !c.enabled {
		return prefix
	}
	return ansiWarningColor + prefix + ansiReset
}

func (c colors) error(prefix string) string {
	if !c.enabled {
		return prefix
	}
	return ansiErrorColor + prefix + ansiReset
}

// success styles text as a confirmation, such as a check mark or an ok state.
func (c colors) success(text string) string {
	if !c.enabled {
		return text
	}
	return ansiSuccessColor + text + ansiReset
}

// warn styles text as a warning without the status-column alignment.
func (c colors) warn(text string) string {
	if !c.enabled {
		return text
	}
	return ansiWarningColor + text + ansiReset
}

// dim styles text as a secondary detail, such as a file path or a hint.
func (c colors) dim(text string) string {
	if !c.enabled {
		return text
	}
	return ansiDimColor + text + ansiReset
}

// styledLines wraps w so every complete line is emitted in style, for writers that
// format plain text themselves (subprocess diagnostics). nil stays nil and a
// non-terminal writer is left uncolored.
func styledLines(w io.Writer, style string) io.Writer {
	if w == nil {
		return nil
	}
	return lineWriter{dst: w, style: style, on: colorEnabled(color, isTerminal(w))}
}

// lineWriter applies a style to each written line, resetting before every newline.
type lineWriter struct {
	dst   io.Writer
	style string
	on    bool
}

func (w lineWriter) Write(p []byte) (int, error) {
	if !w.on {
		return w.dst.Write(p)
	}
	text := w.style + strings.ReplaceAll(string(p), "\n", ansiReset+"\n"+w.style)
	if len(p) > 0 && p[len(p)-1] != '\n' {
		text += ansiReset
	}
	if _, err := io.WriteString(w.dst, text); err != nil {
		return 0, err
	}
	return len(p), nil
}

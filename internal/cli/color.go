package cli

import (
	"fmt"
	"io"
	"os"
)

const (
	ansiReset        = "\x1b[0m"
	ansiHeaderColor  = "\x1b[1;35m"
	ansiStatusColor  = "\x1b[1;36m"
	ansiWarningColor = "\x1b[1;33m"
	ansiErrorColor   = "\x1b[1;31m"
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

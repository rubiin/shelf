package cli

import (
	"fmt"
	"io"
	"os"
)

const (
	ansiReset       = "\x1b[0m"
	ansiHeaderColor = "\x1b[1;35m"
	ansiStatusColor = "\x1b[1;36m"
	statusWidth     = 10
)

// colorEnabled reports whether ANSI colors should be emitted for the
// requested mode: always, never, or auto.
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

// colors formats diagnostics prefixes with or without ANSI colors. Headers
// are bold magenta and statuses are bold cyan, matching sheldon.
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

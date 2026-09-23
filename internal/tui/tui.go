// Package tui implements shelf's interactive prompts: the checkbox picker behind
// remove --interactive and the line prompts behind init.
package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// ErrCancelled is returned when the user aborts the picker with q, escape, or ctrl+c.
var ErrCancelled = errors.New("selection cancelled")

// Terminal abstracts raw-mode control so tests can script keystrokes.
type Terminal interface {
	MakeRaw(int) (*term.State, error)
	Restore(int, *term.State) error
}

// IO is the picker's input, output, and terminal hooks.
type IO struct {
	In      io.Reader
	Out     io.Writer
	MakeRaw func(int) (*term.State, error)
	Restore func(int, *term.State) error
	Color   bool
	// IsTerminal reports whether a file descriptor is an interactive terminal.
	// Nil falls back to term.IsTerminal; tests set it to script the picker
	// through pipes and buffers.
	IsTerminal func(fd int) bool
}

const hintLine = "  ↑/↓ or j/k move · space toggle · a all · enter confirm · q cancel"

// terminalStream reports whether stream is an interactive terminal. File
// streams are checked directly, nil streams are rejected, and non-file streams
// pass only when a check is configured (tests drive the picker through buffers
// and script the check via the IO seam).
func terminalStream(stream any, check func(int) bool) bool {
	if stream == nil {
		return false
	}
	if file, ok := stream.(*os.File); ok {
		return check(int(file.Fd()))
	}
	return check != nil
}

// terminalWidth returns the terminal's column count; 0 when it cannot be
// determined. A var so tests can pin a width for redraw checks.
var terminalWidth = func(fd int) int {
	width, _, err := term.GetSize(fd)
	if err != nil {
		return 0
	}
	return width
}

// Select shows an interactive checkbox list and returns the chosen options in their original order.
func Select(options []string, io IO) ([]string, error) {
	if len(options) == 0 {
		return nil, errors.New("no options to select")
	}
	check := io.IsTerminal
	if check == nil {
		check = term.IsTerminal
	}
	// Refuse redirected or piped streams before any raw-mode or ANSI work, so
	// `shelf remove --interactive > file` fails instead of dumping escapes.
	if !terminalStream(io.In, check) || !terminalStream(io.Out, check) {
		return nil, errors.New("interactive selection requires a terminal")
	}
	fd := os.Stdin.Fd()
	if file, ok := io.In.(*os.File); ok {
		fd = file.Fd()
	}
	state, err := io.MakeRaw(int(fd))
	if err != nil {
		return nil, fmt.Errorf("interactive selection requires a terminal: %w", err)
	}
	// A panic anywhere in the picker must not leave the terminal raw; restore
	// the previous mode before the panic keeps unwinding.
	defer func() {
		if r := recover(); r != nil {
			_ = io.Restore(int(fd), state)
			panic(r)
		}
	}()
	checked := make([]bool, len(options))
	cursor := 0
	outFd := os.Stdout.Fd()
	if file, ok := io.Out.(*os.File); ok {
		outFd = file.Fd()
	}
	width := terminalWidth(int(outFd))
	render(io.Out, options, checked, cursor, false, io.Color, width)
	selection, loopErr := readKeys(io.In, options, checked, &cursor, func() {
		render(io.Out, options, checked, cursor, true, io.Color, width)
	})
	restoreErr := io.Restore(int(fd), state)
	_, _ = fmt.Fprint(io.Out, "\r\n")
	if loopErr != nil {
		return nil, errors.Join(loopErr, restoreErr)
	}
	return selection, restoreErr
}

func readKeys(in io.Reader, options []string, checked []bool, cursor *int, redraw func()) ([]string, error) {
	reader := bufio.NewReader(in)
	for {
		key, err := reader.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("read key: %w", err)
		}
		done, err := handleKey(reader, in, key, options, checked, cursor, redraw)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
	}
	var selection []string
	for index, name := range options {
		if checked[index] {
			selection = append(selection, name)
		}
	}
	return selection, nil
}

func handleKey(in *bufio.Reader, raw io.Reader, key byte, options []string, checked []bool, cursor *int, redraw func()) (bool, error) {
	switch key {
	case '\r', '\n':
		return true, nil
	case ' ':
		checked[*cursor] = !checked[*cursor]
	case 'a', 'A':
		for index := range checked {
			checked[index] = !checked[index]
		}
	case 'j':
		if *cursor < len(options)-1 {
			*cursor++
		}
	case 'k':
		if *cursor > 0 {
			*cursor--
		}
	case 'q':
		return false, ErrCancelled
	case 0x03:
		return false, ErrCancelled
	case 0x1b:
		return handleEscape(in, raw, options, checked, cursor, redraw)
	default:
		return false, nil
	}
	redraw()
	return false, nil
}

// escapeGrace is how long a lone ESC waits for the rest of an escape sequence
// before being treated as cancel. Terminals over slow PTYs can split ESC [ A
// across separate reads; the wait reassembles them.
const escapeGrace = 10 * time.Millisecond

func handleEscape(in *bufio.Reader, raw io.Reader, options []string, checked []bool, cursor *int, redraw func()) (bool, error) {
	prefix, err := readEscapeByte(in, raw)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, ErrCancelled
		}
		return false, fmt.Errorf("read escape sequence: %w", err)
	}
	if prefix != '[' {
		// Unrecognized prefixes (Alt+key) are ignored, not a cancel.
		return false, nil
	}
	sequence, err := readEscapeByte(in, raw)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, ErrCancelled
		}
		return false, fmt.Errorf("read escape sequence: %w", err)
	}
	switch sequence {
	case 'A':
		if *cursor > 0 {
			*cursor--
		}
	case 'B':
		if *cursor < len(options)-1 {
			*cursor++
		}
	default:
		// Unsupported sequences are ignored, not a cancel.
		return false, nil
	}
	redraw()
	return false, nil
}

// readEscapeByte returns the next byte of an escape sequence, waiting up to
// escapeGrace for it when nothing is buffered. A vacuous window means a lone
// ESC, reported as io.EOF.
func readEscapeByte(in *bufio.Reader, raw io.Reader) (byte, error) {
	if in.Buffered() > 0 {
		return in.ReadByte()
	}
	if file, ok := raw.(*os.File); ok {
		// Select resolves the raw fd with File.Fd(), which puts the file in
		// blocking mode. A blocking-mode fd ignores read deadlines (the kernel
		// parks the read until data arrives), so switch to non-blocking for
		// the timed wait and back afterwards.
		_ = syscall.SetNonblock(int(file.Fd()), true)
		_ = file.SetReadDeadline(time.Now().Add(escapeGrace))
		defer func() {
			_ = file.SetReadDeadline(time.Time{})
			_ = syscall.SetNonblock(int(file.Fd()), false)
		}()
	}
	b, err := in.ReadByte()
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) || os.IsTimeout(err) {
			return 0, io.EOF
		}
		return 0, err
	}
	return b, nil
}

const (
	ansiReset      = "\x1b[0m"
	ansiCyan       = "\x1b[36m"
	ansiGreen      = "\x1b[32m"
	ansiDim        = "\x1b[2m"
	clearLineToEnd = "\x1b[K"
)

func render(out io.Writer, options []string, checked []bool, cursor int, redraw, color bool, width int) {
	total := 0
	for _, option := range options {
		total += rowHeight(option, width)
	}
	var builder strings.Builder
	if redraw {
		fmt.Fprintf(&builder, "\x1b[%dA\r", total)
	}
	for index, option := range options {
		box := "[ ]"
		if checked[index] {
			box = "[x]"
			if color {
				box = ansiGreen + box + ansiReset
			}
		}
		row := "  " + box + " " + option + clearLineToEnd
		if index == cursor {
			row = "> " + box + " " + option + clearLineToEnd
			if color {
				row = ansiCyan + row + ansiReset
			}
		}
		builder.WriteString(row + "\r\n")
	}
	hint := hintLine + clearLineToEnd
	if color {
		hint = ansiDim + hint + ansiReset
	}
	builder.WriteString(hint)
	_, _ = fmt.Fprint(out, builder.String())
}

// optionPrefixCells is the display width of a row's box and marker; identical
// for the unchecked, checked, and cursor variants (color escapes are zero-width).
const optionPrefixCells = 6

// rowHeight estimates the terminal rows one option occupies at width columns:
// options that wrap at the right edge or embed newlines take more than one.
func rowHeight(option string, width int) int {
	if width <= 0 {
		return strings.Count(option, "\n") + 1
	}
	rows := 0
	for index, segment := range strings.Split(option, "\n") {
		cells := utf8.RuneCountInString(segment)
		if index == 0 {
			cells += optionPrefixCells
		}
		rows += cells / width
		if cells%width != 0 {
			rows++
		}
	}
	return rows
}

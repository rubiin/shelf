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
}

const hintLine = "  ↑/↓ or j/k move · space toggle · a all · enter confirm · q cancel"

// Select shows an interactive checkbox list and returns the chosen options in their original order.
func Select(options []string, io IO) ([]string, error) {
	if len(options) == 0 {
		return nil, errors.New("no options to select")
	}
	fd := os.Stdin.Fd()
	if file, ok := io.In.(*os.File); ok {
		fd = file.Fd()
	}
	state, err := io.MakeRaw(int(fd))
	if err != nil {
		return nil, fmt.Errorf("interactive selection requires a terminal: %w", err)
	}
	checked := make([]bool, len(options))
	cursor := 0
	render(io.Out, options, checked, cursor, false, io.Color)
	selection, loopErr := readKeys(io.In, options, checked, &cursor, func() {
		render(io.Out, options, checked, cursor, true, io.Color)
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
		done, err := handleKey(reader, key, options, checked, cursor, redraw)
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

func handleKey(in *bufio.Reader, key byte, options []string, checked []bool, cursor *int, redraw func()) (bool, error) {
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
		return handleEscape(in, options, checked, cursor, redraw)
	default:
		return false, nil
	}
	redraw()
	return false, nil
}

func handleEscape(in *bufio.Reader, options []string, checked []bool, cursor *int, redraw func()) (bool, error) {
	// A lone ESC has nothing buffered and cancels; arrow keys arrive as ESC [ A in one burst.
	if in.Buffered() == 0 {
		return false, ErrCancelled
	}
	prefix, err := in.ReadByte()
	if err != nil {
		return false, ErrCancelled
	}
	if prefix != '[' {
		// Unrecognized prefixes (Alt+key) are ignored, not a cancel.
		return false, nil
	}
	if in.Buffered() == 0 {
		return false, ErrCancelled
	}
	sequence, err := in.ReadByte()
	if err != nil {
		return false, ErrCancelled
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

const (
	ansiReset      = "\x1b[0m"
	ansiCyan       = "\x1b[36m"
	ansiGreen      = "\x1b[32m"
	ansiDim        = "\x1b[2m"
	clearLineToEnd = "\x1b[K"
)

func render(out io.Writer, options []string, checked []bool, cursor int, redraw, color bool) {
	var builder strings.Builder
	if redraw {
		fmt.Fprintf(&builder, "\x1b[%dA\r", len(options))
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

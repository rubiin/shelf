// Package tui implements the interactive terminal picker used by commands
// such as remove --interactive.
package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrCancelled is returned when the user aborts the picker with q,
// escape, or ctrl+c.
var ErrCancelled = errors.New("selection cancelled")

// Terminal abstracts raw-mode control so the picker can be tested with
// scripted keystrokes.
type Terminal interface {
	MakeRaw(int) (*term.State, error)
	Restore(int, *term.State) error
}

// IO carries the picker's input, output, and terminal control.
type IO struct {
	In      io.Reader
	Out     io.Writer
	MakeRaw func(int) (*term.State, error)
	Restore func(int, *term.State) error
	Color   bool
}

const hintLine = "  ↑/↓ or j/k move · space toggle · a all · enter confirm · q cancel"

// Select shows an interactive checkbox list and returns the chosen options
// in their original order. It returns ErrCancelled when the user aborts.
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
	buffer := make([]byte, 1)
	for {
		if _, err := in.Read(buffer); err != nil {
			return nil, fmt.Errorf("read key: %w", err)
		}
		done, err := handleKey(in, buffer[0], options, checked, cursor, redraw)
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

func handleKey(in io.Reader, key byte, options []string, checked []bool, cursor *int, redraw func()) (bool, error) {
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

func handleEscape(in io.Reader, options []string, checked []bool, cursor *int, redraw func()) (bool, error) {
	var sequence [1]byte
	if _, err := in.Read(sequence[:]); err != nil {
		return false, ErrCancelled
	}
	if sequence[0] != '[' {
		return false, ErrCancelled
	}
	if _, err := in.Read(sequence[:]); err != nil {
		return false, ErrCancelled
	}
	switch sequence[0] {
	case 'A':
		if *cursor > 0 {
			*cursor--
		}
	case 'B':
		if *cursor < len(options)-1 {
			*cursor++
		}
	default:
		return false, ErrCancelled
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

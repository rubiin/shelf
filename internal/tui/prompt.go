package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Choose shows a numbered menu of options and returns the option the user
// picks, matched by number or case-insensitive name. Empty and unrecognized
// answers re-prompt until a valid choice is made or reading input fails.
func Choose(question string, options []string, in *bufio.Reader, out io.Writer) (string, error) {
	if len(options) == 0 {
		return "", errors.New("no options to choose from")
	}
	for {
		_, _ = fmt.Fprintln(out, question)
		for index, option := range options {
			_, _ = fmt.Fprintf(out, "  %d) %s\n", index+1, option)
		}
		_, _ = fmt.Fprint(out, "> ")
		answer, err := readLine(in)
		if err != nil {
			return "", fmt.Errorf("read choice: %w", err)
		}
		if choice, ok := matchOption(answer, options); ok {
			return choice, nil
		}
		if answer == "" {
			_, _ = fmt.Fprintln(out, "invalid choice")
		} else {
			_, _ = fmt.Fprintf(out, "invalid choice: %s\n", answer)
		}
	}
}

// Confirm asks a y/N question and reports whether the user agreed. Only y or
// yes (case-insensitive) confirms; an empty answer, n, or no declines, and any
// other answer re-prompts until a valid one is given or reading input fails.
func Confirm(question string, in *bufio.Reader, out io.Writer) (bool, error) {
	for {
		_, _ = fmt.Fprintf(out, "%s [y/N] ", question)
		answer, err := readLine(in)
		if err != nil {
			return false, fmt.Errorf("read answer: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			return true, nil
		case "", "n", "no":
			return false, nil
		default:
			_, _ = fmt.Fprintf(out, "invalid answer: %s\n", answer)
		}
	}
}

// matchOption accepts an answer as either an option number or its name.
func matchOption(answer string, options []string) (string, bool) {
	answer = strings.ToLower(strings.TrimSpace(answer))
	for index, option := range options {
		if answer == fmt.Sprintf("%d", index+1) || answer == strings.ToLower(option) {
			return option, true
		}
	}
	return "", false
}

// readLine reads one line, stripping the trailing newline. Input ending at EOF
// without a newline is still returned; EOF with no input at all is an error.
func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

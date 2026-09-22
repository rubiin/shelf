package tui

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func runChoose(t *testing.T, input, question string, options []string) (string, string, error) {
	t.Helper()
	var output bytes.Buffer
	choice, err := Choose(question, options, bufio.NewReader(strings.NewReader(input)), &output)
	return choice, output.String(), err
}

func runConfirm(t *testing.T, input, question string) (bool, string, error) {
	t.Helper()
	var output bytes.Buffer
	confirmed, err := Confirm(question, bufio.NewReader(strings.NewReader(input)), &output)
	return confirmed, output.String(), err
}

func TestChooseRejectsNoOptions(t *testing.T) {
	_, _, err := runChoose(t, "1", "Select:", nil)
	if err == nil || !strings.Contains(err.Error(), "no options") {
		t.Fatalf("err = %v, want no-options error", err)
	}
}

func TestChooseByNumber(t *testing.T) {
	choice, output, err := runChoose(t, "2\n", "Select shell:", []string{"zsh", "bash"})
	if err != nil {
		t.Fatal(err)
	}
	if choice != "bash" {
		t.Fatalf("choice = %q, want bash", choice)
	}
	if !strings.Contains(output, "Select shell:") || !strings.Contains(output, "  1) zsh") || !strings.Contains(output, "  2) bash") || !strings.HasSuffix(output, "> ") {
		t.Fatalf("output = %q, want question, numbered options, and prompt", output)
	}
}

func TestChooseByName(t *testing.T) {
	for _, test := range []struct {
		answer string
		want   string
	}{
		{"zsh\n", "zsh"},
		{"ZSH\n", "zsh"},
		{"  bash \n", "bash"},
	} {
		choice, _, err := runChoose(t, test.answer, "Select:", []string{"zsh", "bash"})
		if err != nil {
			t.Fatal(err)
		}
		if choice != test.want {
			t.Fatalf("answer %q choice = %q, want %q", test.answer, choice, test.want)
		}
	}
}

func TestChooseRepromptsOnInvalidChoice(t *testing.T) {
	choice, output, err := runChoose(t, "fig\n\n1\n", "Select:", []string{"zsh", "bash"})
	if err != nil {
		t.Fatal(err)
	}
	if choice != "zsh" {
		t.Fatalf("choice = %q, want zsh after invalid answers", choice)
	}
	if !strings.Contains(output, "invalid choice: fig") || !strings.Contains(output, "invalid choice\n") {
		t.Fatalf("output missing invalid-choice messages: %q", output)
	}
}

func TestChooseReportsReadError(t *testing.T) {
	_, _, err := runChoose(t, "", "Select:", []string{"zsh", "bash"})
	if err == nil || !strings.Contains(err.Error(), "read choice") {
		t.Fatalf("err = %v, want read-choice error", err)
	}
}

func TestConfirmYes(t *testing.T) {
	for _, answer := range []string{"y\n", "yes\n", "Y\n", "YES\n", "  y \n"} {
		confirmed, output, err := runConfirm(t, answer, "Initialize?")
		if err != nil {
			t.Fatal(err)
		}
		if !confirmed {
			t.Fatalf("answer %q confirmed = false, want true", answer)
		}
		if !strings.Contains(output, "Initialize? [y/N]") {
			t.Fatalf("answer %q output = %q, missing prompt", answer, output)
		}
	}
}

func TestConfirmNo(t *testing.T) {
	for _, answer := range []string{"n\n", "no\n", "\n", "N\n"} {
		confirmed, _, err := runConfirm(t, answer, "Initialize?")
		if err != nil {
			t.Fatal(err)
		}
		if confirmed {
			t.Fatalf("answer %q confirmed = true, want false", answer)
		}
	}
}

func TestConfirmRepromptsOnInvalidAnswer(t *testing.T) {
	confirmed, output, err := runConfirm(t, "maybe\n\n", "Initialize?")
	if err != nil {
		t.Fatal(err)
	}
	if confirmed {
		t.Fatal("confirmed = true, want false for empty answer")
	}
	if !strings.Contains(output, "invalid answer: maybe") {
		t.Fatalf("output missing invalid-answer message: %q", output)
	}
}

func TestConfirmReportsReadError(t *testing.T) {
	_, _, err := runConfirm(t, "", "Initialize?")
	if err == nil || !strings.Contains(err.Error(), "read answer") {
		t.Fatalf("err = %v, want read-answer error", err)
	}
}

package tui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"golang.org/x/term"
)

type fakeTerminal struct {
	madeRaw  bool
	restored bool
}

func (f *fakeTerminal) MakeRaw(int) (*term.State, error) {
	f.madeRaw = true
	return &term.State{}, nil
}

func (f *fakeTerminal) Restore(int, *term.State) error {
	f.restored = true
	return nil
}

func runSelect(keys string, choices []string) ([]string, *fakeTerminal, string, error) {
	terminal := &fakeTerminal{}
	var output bytes.Buffer
	selected, err := Select(choices, IO{In: strings.NewReader(keys), Out: &output, MakeRaw: terminal.MakeRaw, Restore: terminal.Restore})
	return selected, terminal, output.String(), err
}

func TestSelectTogglesAndConfirms(t *testing.T) {
	selected, terminal, _, err := runSelect("\x1b[B \r", []string{"alpha", "beta", "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "beta" {
		t.Fatalf("selected = %v, want [beta]", selected)
	}
	if !terminal.madeRaw || !terminal.restored {
		t.Fatalf("raw mode not managed: madeRaw=%v restored=%v", terminal.madeRaw, terminal.restored)
	}
}

func TestSelectSpaceTogglesFirst(t *testing.T) {
	selected, _, _, err := runSelect(" \r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "alpha" {
		t.Fatalf("selected = %v, want [alpha]", selected)
	}
}

func TestSelectAllTogglesEverything(t *testing.T) {
	selected, _, _, err := runSelect("a\r", []string{"alpha", "beta", "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 3 {
		t.Fatalf("selected = %v, want all three", selected)
	}
	selected, _, _, err = runSelect(" a\r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "beta" {
		t.Fatalf("second toggle-all = %v, want only beta", selected)
	}
}

func TestSelectJKNavigation(t *testing.T) {
	selected, _, _, err := runSelect("jjk \r", []string{"alpha", "beta", "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "beta" {
		t.Fatalf("selected = %v, want [beta]", selected)
	}
}

func TestSelectNavigationStopsAtEdges(t *testing.T) {
	selected, _, _, err := runSelect("kk \r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "alpha" {
		t.Fatalf("selected = %v, want [alpha]", selected)
	}
}

func TestSelectCancelKeys(t *testing.T) {
	for _, keys := range []string{"q", "\x03"} {
		selected, terminal, _, err := runSelect(keys, []string{"alpha"})
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("keys %q err = %v, want ErrCancelled", keys, err)
		}
		if selected != nil {
			t.Fatalf("keys %q selected = %v, want nil", keys, selected)
		}
		if !terminal.restored {
			t.Fatalf("keys %q did not restore terminal", keys)
		}
	}
}

func TestSelectRendersCheckboxes(t *testing.T) {
	_, _, output, err := runSelect("\x1b[B \x1b[A\r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "[ ] alpha") {
		t.Fatalf("missing unchecked alpha: %q", output)
	}
	if !strings.Contains(output, "[x] beta") {
		t.Fatalf("missing checked beta: %q", output)
	}
}

func TestSelectRequiresTerminal(t *testing.T) {
	_, err := Select([]string{"alpha"}, IO{In: strings.NewReader("\r"), Out: &bytes.Buffer{}, MakeRaw: func(int) (*term.State, error) {
		return nil, errors.New("inappropriate ioctl for device")
	}, Restore: func(int, *term.State) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("err = %v, want terminal error", err)
	}
}

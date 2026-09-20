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

func runSelect(t *testing.T, keys string, choices []string) ([]string, *fakeTerminal, string, error) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	terminal := &fakeTerminal{}
	var output bytes.Buffer
	selected, err := Select(choices, IO{In: strings.NewReader(keys), Out: &output, MakeRaw: terminal.MakeRaw, Restore: terminal.Restore, Color: true})
	return selected, terminal, output.String(), err
}

func TestSelectTogglesAndConfirms(t *testing.T) {
	selected, terminal, _, err := runSelect(t, "\x1b[B \r", []string{"alpha", "beta", "gamma"})
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
	selected, _, _, err := runSelect(t, " \r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "alpha" {
		t.Fatalf("selected = %v, want [alpha]", selected)
	}
}

func TestSelectAllTogglesEverything(t *testing.T) {
	selected, _, _, err := runSelect(t, "a\r", []string{"alpha", "beta", "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 3 {
		t.Fatalf("selected = %v, want all three", selected)
	}
	selected, _, _, err = runSelect(t, " a\r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "beta" {
		t.Fatalf("second toggle-all = %v, want only beta", selected)
	}
}

func TestSelectJKNavigation(t *testing.T) {
	selected, _, _, err := runSelect(t, "jjk \r", []string{"alpha", "beta", "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "beta" {
		t.Fatalf("selected = %v, want [beta]", selected)
	}
}

func TestSelectNavigationStopsAtEdges(t *testing.T) {
	selected, _, _, err := runSelect(t, "kk \r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "alpha" {
		t.Fatalf("selected = %v, want [alpha]", selected)
	}
}

func TestSelectCancelKeys(t *testing.T) {
	for _, keys := range []string{"q", "\x03"} {
		selected, terminal, _, err := runSelect(t, keys, []string{"alpha"})
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
	_, _, output, err := runSelect(t, "\x1b[B \x1b[A\r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "[ ] alpha") {
		t.Fatalf("missing unchecked alpha: %q", output)
	}
	if !strings.Contains(output, "\x1b[32m[x]\x1b[0m beta") {
		t.Fatalf("missing green checked beta: %q", output)
	}
}

func TestSelectFirstRenderUsesCarriageReturns(t *testing.T) {
	_, _, output, err := runSelect(t, "\r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	// In raw mode a bare \n keeps the cursor column, which staircases the
	// rows; rows must be separated by \r\n.
	want := "\x1b[36m> [ ] alpha\x1b[K\x1b[0m\r\n  [ ] beta\x1b[K\r\n"
	if !strings.HasPrefix(output, want) {
		t.Fatalf("first render = %q, want prefix %q", output, want)
	}
}

func TestSelectRedrawMovesUpOptionCount(t *testing.T) {
	_, _, output, err := runSelect(t, "\x1b[B\r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	// Two rows are drawn (both options plus hint) and the cursor rests on
	// the hint row, so redraw must move up exactly len(options) rows.
	if !strings.Contains(output, "\x1b[2A\r") {
		t.Fatalf("redraw escape missing: %q", output)
	}
}

func TestSelectColorsEnabled(t *testing.T) {
	_, _, output, err := runSelect(t, "\r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	// Cursor row in cyan.
	if !strings.Contains(output, "\x1b[36m> [ ] alpha\x1b[K\x1b[0m") {
		t.Fatalf("missing cyan cursor row: %q", output)
	}
	if strings.Contains(output, "\x1b[36m  [ ] beta") {
		t.Fatalf("non-cursor row colored cyan: %q", output)
	}
}

func TestSelectColorsCheckedGreen(t *testing.T) {
	_, _, output, err := runSelect(t, " \r", []string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\x1b[32m[x]\x1b[0m alpha") {
		t.Fatalf("missing green checked box: %q", output)
	}
	if strings.Contains(output, "\x1b[32m[ ]") {
		t.Fatalf("unchecked box colored green: %q", output)
	}
}

func TestSelectColorsHintDim(t *testing.T) {
	_, _, output, err := runSelect(t, "\r", []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\x1b[2m  ↑/↓ or j/k move · space toggle · a all · enter confirm · q cancel\x1b[K\x1b[0m") {
		t.Fatalf("missing dim hint line: %q", output)
	}
}

func TestSelectColorsDisabledWithoutColor(t *testing.T) {
	terminal := &fakeTerminal{}
	var output bytes.Buffer
	_, err := Select([]string{"alpha"}, IO{In: strings.NewReader("\r"), Out: &output, MakeRaw: terminal.MakeRaw, Restore: terminal.Restore})
	if err != nil {
		t.Fatal(err)
	}
	for _, escape := range []string{"\x1b[0m", "\x1b[36m", "\x1b[32m", "\x1b[2m"} {
		if strings.Contains(output.String(), escape) {
			t.Fatalf("colorless IO emitted color escape %q: %q", escape, output.String())
		}
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

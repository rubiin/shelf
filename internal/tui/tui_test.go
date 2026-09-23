package tui

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

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

func TestSelectBareEscapeCancels(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readPipe.Close() }()
	defer func() { _ = writePipe.Close() }()

	terminal := &fakeTerminal{}
	var output bytes.Buffer
	finished := make(chan error, 1)
	go func() {
		_, err := Select([]string{"alpha", "beta"}, IO{In: readPipe, Out: &output, MakeRaw: terminal.MakeRaw, Restore: terminal.Restore, IsTerminal: func(int) bool { return true }})
		finished <- err
	}()
	if _, err := writePipe.Write([]byte{0x1b}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-finished:
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("bare escape err = %v, want ErrCancelled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a lone escape press did not cancel the picker")
	}
	if !terminal.restored {
		t.Fatal("terminal not restored after bare escape")
	}
}

func TestSelectEscapePrefixKeepsSelection(t *testing.T) {
	// Alt+key and unsupported escape sequences are ignored, not treated as a cancel.
	for _, keys := range []string{"\x1bx \r", "\x1b[Z \r"} {
		selected, _, _, err := runSelect(t, keys, []string{"alpha", "beta"})
		if err != nil {
			t.Fatalf("keys %q err = %v", keys, err)
		}
		if len(selected) != 1 || selected[0] != "alpha" {
			t.Fatalf("keys %q selected = %v, want [alpha]", keys, selected)
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
	// Rows must be separated by \r\n: in raw mode a bare \n keeps the cursor column.
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
	// The cursor rests on the hint row, so redraw moves up exactly len(options) rows.
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

func TestSelectEscapeSplitAcrossReads(t *testing.T) {
	// A slow PTY can deliver ESC, [, and the final byte in separate reads; the
	// grace period must reassemble an arrow key instead of canceling.
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readPipe.Close() }()
	defer func() { _ = writePipe.Close() }()

	terminal := &fakeTerminal{}
	var output bytes.Buffer
	finished := make(chan error, 1)
	go func() {
		_, err := Select([]string{"alpha", "beta"}, IO{In: readPipe, Out: &output, MakeRaw: terminal.MakeRaw, Restore: terminal.Restore, IsTerminal: func(int) bool { return true }})
		finished <- err
	}()

	// One byte per write, spaced out, so each lands in its own read.
	for _, b := range []byte{0x1b, '[', 'B', ' '} {
		if _, err := writePipe.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if _, err := writePipe.Write([]byte{'\r'}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-finished:
		if errors.Is(err, ErrCancelled) {
			t.Fatal("escape sequence split across reads was misread as a cancel")
		}
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("picker did not finish after a split escape sequence")
	}
	if !terminal.restored {
		t.Fatal("terminal not restored")
	}
	// ESC [ B moved down, space toggled beta, so a checkbox is marked.
	if !strings.Contains(output.String(), "[x] beta") {
		t.Fatalf("split escape sequence did not navigate: %q", output.String())
	}
}

func TestSelectRedrawCountsPhysicalLines(t *testing.T) {
	oldWidth := terminalWidth
	terminalWidth = func(int) int { return 10 }
	defer func() { terminalWidth = oldWidth }()

	tests := []struct {
		name    string
		options []string
		want    string
	}{
		{"wrapped option", []string{"a very long option that wraps", "beta"}, "\x1b[5A\r"},
		{"newline option", []string{"alpha\nbeta", "g"}, "\x1b[4A\r"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, output, err := runSelect(t, "\x1b[B \r", tt.options)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, tt.want) {
				t.Fatalf("redraw escape missing %q: %q", tt.want, output)
			}
		})
	}
}

func TestSelectRejectsRedirectedOutput(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	_, err = Select([]string{"alpha"}, IO{
		In:      strings.NewReader("\r"),
		Out:     file,
		MakeRaw: func(int) (*term.State, error) { return &term.State{}, nil },
		Restore: func(int, *term.State) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("redirected output err = %v, want requires-terminal error", err)
	}
	if got, rerr := os.ReadFile(file.Name()); rerr != nil || len(got) != 0 {
		t.Fatalf("redirected output received ANSI bytes: %q", got)
	}
}

func TestSelectZeroValueIOFailsCleanly(t *testing.T) {
	_, err := Select([]string{"alpha"}, IO{})
	if err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("zero-value IO err = %v, want requires-terminal error", err)
	}
}

type panickingReader struct{}

func (panickingReader) Read([]byte) (int, error) {
	panic("reader exploded")
}

func TestSelectRestoresTerminalOnPanic(t *testing.T) {
	terminal := &fakeTerminal{}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected the picker's panic to propagate")
		}
		if !terminal.restored {
			t.Fatal("terminal not restored after a picker panic")
		}
	}()
	_, _ = Select([]string{"alpha"}, IO{
		In:         panickingReader{},
		Out:        &bytes.Buffer{},
		MakeRaw:    terminal.MakeRaw,
		Restore:    terminal.Restore,
		IsTerminal: func(int) bool { return true },
	})
}

// lockedBuffer lets the test poll picker output while Select writes it from another goroutine.
type lockedBuffer struct {
	mutex sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedBuffer) Len() int {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.Buffer.Len()
}

func TestSelectRestoresTerminalBeforeAFatalSignal(t *testing.T) {
	reraised := make(chan os.Signal, 1)
	restoredFirst := make(chan bool, 1)
	var restored atomic.Bool
	original := reraise
	reraise = func(sig os.Signal) {
		restoredFirst <- restored.Load()
		reraised <- sig
	}
	t.Cleanup(func() { reraise = original })

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readPipe.Close() }()
	output := &lockedBuffer{}
	finished := make(chan error, 1)
	go func() {
		_, err := Select([]string{"alpha"}, IO{
			In:         readPipe,
			Out:        output,
			MakeRaw:    func(int) (*term.State, error) { return &term.State{}, nil },
			Restore:    func(int, *term.State) error { restored.Store(true); return nil },
			IsTerminal: func(int) bool { return true },
		})
		finished <- err
	}()
	// The first render happens after the guard is installed.
	deadline := time.Now().Add(2 * time.Second)
	for output.Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("picker never rendered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-reraised:
		if sig != syscall.SIGHUP {
			t.Fatalf("reraised %v, want SIGHUP", sig)
		}
		if !<-restoredFirst {
			t.Fatal("signal re-raised before the terminal was restored")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SIGHUP during the blocking read was not handled")
	}
	_ = writePipe.Close()
	if err := <-finished; err == nil {
		t.Fatal("Select succeeded after its input closed")
	}
}

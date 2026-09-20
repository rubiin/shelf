package lock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectFilesUsesFirstMatchForShellDefaults(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{
		"install_test_zsh.sh",
		"zsh-autosuggestions.plugin.zsh",
		"zsh-autosuggestions.zsh",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := selectFiles(directory, "zsh-autosuggestions", "zsh", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, "zsh-autosuggestions.plugin.zsh")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s]", got, want)
	}
}

func TestSelectFilesCollectsExplicitUsePatterns(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{
		"README.md",
		"demo.plugin.zsh",
		"demo.zsh",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh", "*.md"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected all explicit matches, got %v", got)
	}
}

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInlineLockAndSource(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Execute([]string{"source"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "echo testing") {
		t.Fatalf("source output = %q", output.String())
	}
}

func TestLockStoresLockfileInConfigDirectory(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "plugins.lock")); err != nil {
		t.Fatalf("lock file missing in config directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "plugins.lock")); err == nil {
		t.Fatal("lock file was written under the data directory")
	}
}

func TestLockReportsProgressOnStderr(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"lock"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	func() {
		var stderr bytes.Buffer
		if err := Execute([]string{"lock", "--color", "always"}, &bytes.Buffer{}, &stderr); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"\x1b[1;35mLoaded\x1b[0m", "\x1b[1;36m   Checked\x1b[0m", "\x1b[1;35mLocked\x1b[0m"} {
			if !strings.Contains(stderr.String(), expected) {
				t.Errorf("colored lock diagnostics missing %q: %q", expected, stderr.String())
			}
		}
	}()
	func() {
		var stderr bytes.Buffer
		if err := Execute([]string{"lock", "--color", "never"}, &bytes.Buffer{}, &stderr); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(stderr.String(), '\x1b') {
			t.Errorf("--color never emitted escape sequences: %q", stderr.String())
		}
		for _, expected := range []string{"Loaded ", "Locked "} {
			if !strings.Contains(stderr.String(), expected) {
				t.Errorf("--color never diagnostics missing %q: %q", expected, stderr.String())
			}
		}
	}()
	func() {
		var stderr bytes.Buffer
		if err := Execute([]string{"lock", "--update"}, &bytes.Buffer{}, &stderr); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(stderr.String(), '\x1b') {
			t.Errorf("auto color emitted escapes to a non-terminal: %q", stderr.String())
		}
	}()
	if stdout.Len() != 0 {
		t.Fatalf("lock stdout = %q", stdout.String())
	}
}

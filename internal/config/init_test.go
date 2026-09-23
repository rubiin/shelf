package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeCreatesBashConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := Initialize(path, Bash); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != DefaultConfig(Bash) {
		t.Fatalf("config contents = %q", contents)
	}
}

func TestInitializeDoesNotOverwriteExistingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("# user config\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Initialize(path, Zsh); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(original) {
		t.Fatalf("existing config overwritten: %q", contents)
	}
}

func TestInitializeRefusesADirectoryAtTheConfigPath(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.toml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Initialize(path, Bash); err == nil {
		t.Fatal("initialize accepted a directory as the config path")
	} else if !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("error = %v, want a directory refusal", err)
	}
	if entries, err := os.ReadDir(directory); err != nil {
		t.Fatal(err)
	} else if len(entries) != 1 {
		t.Errorf("directory contents changed: %v", entries)
	}
}

func TestInitializeWritesAtomically(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "nested", "config.toml")
	if err := Initialize(path, Zsh); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != DefaultConfig(Zsh) {
		t.Fatalf("config contents = %q", contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	// A temp file must never survive a completed init.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config-") && strings.HasSuffix(entry.Name(), ".toml") {
			t.Errorf("temporary file %s survived the init", entry.Name())
		}
	}
}

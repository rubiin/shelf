package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeCreatesBashConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "plugins.toml")
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
	path := filepath.Join(t.TempDir(), "plugins.toml")
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

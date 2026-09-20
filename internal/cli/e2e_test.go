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
	configFile := filepath.Join(directory, "plugins.toml")
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

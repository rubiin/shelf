package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddAndRemovePreserveUnrelatedTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"bash\"\n\n[templates]\ncustom = \"source {file}\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, "local", RawPlugin{Local: "plugins/local"}); err != nil {
		t.Fatal(err)
	}
	contents, _ := os.ReadFile(path)
	if !strings.Contains(string(contents), "custom = \"source {file}\"") || !strings.Contains(string(contents), "[plugins.local]") {
		t.Fatalf("add lost content: %s", contents)
	}
	if err := Remove(path, "local"); err != nil {
		t.Fatal(err)
	}
	contents, _ = os.ReadFile(path)
	if !strings.Contains(string(contents), "custom = \"source {file}\"") || strings.Contains(string(contents), "[plugins.local]") {
		t.Fatalf("remove changed wrong content: %s", contents)
	}
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveIncludesPluginSubtables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\n[plugins.foo]\ngithub = \"x/y\"\n\n[plugins.foo.hooks]\npre = \"echo hi\"\n\n[plugins.bar]\ngithub = \"a/b\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "foo"); err != nil {
		t.Fatal(err)
	}
	contents, _ := os.ReadFile(path)
	if strings.Contains(string(contents), "plugins.foo") {
		t.Fatalf("remove left foo content behind: %s", contents)
	}
	if !strings.Contains(string(contents), "[plugins.bar]") {
		t.Fatalf("remove lost unrelated plugin: %s", contents)
	}
}

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

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndValidatePluginSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "shell = \"bash\"\n\n[plugins.fzf]\ngithub = \"junegunn/fzf\"\nuse = [\"shell/*.bash\"]\napply = [\"source {file}\"]\nprofiles = [\"work\"]\nhooks = { post = \"echo loaded\" }\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	plugin, ok := cfg.Plugins["fzf"]
	if !ok || plugin.GitHub != "junegunn/fzf" || len(plugin.Use) != 1 || plugin.Profiles[0] != "work" || plugin.Hooks["post"] != "echo loaded" {
		t.Fatalf("unexpected plugin: %+v", plugin)
	}
}

func TestValidateRejectsMultipleSources(t *testing.T) {
	cfg := Config{Plugins: map[string]RawPlugin{"bad": {GitHub: "a/b", Remote: "https://example.test/x"}}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected multiple source error")
	}
}

func TestLoadPreservesPluginDeclarationOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[plugins.zsh-defer]\ninline = \"echo defer\"\n\n[plugins.zsh-vi-mode]\ninline = \"echo vi\"\n\n[plugins.powerlevel10k]\ninline = \"echo prompt\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zsh-defer", "zsh-vi-mode", "powerlevel10k"}
	if len(cfg.PluginOrder) != len(want) {
		t.Fatalf("plugin order = %v, want %v", cfg.PluginOrder, want)
	}
	for index := range want {
		if cfg.PluginOrder[index] != want[index] {
			t.Fatalf("plugin order = %v, want %v", cfg.PluginOrder, want)
		}
	}
}

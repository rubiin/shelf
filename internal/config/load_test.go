package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadFoldsLegacyProtocolIntoProto(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[plugins.legacy]\ngithub = \"a/b\"\nprotocol = \"ssh\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plugins["legacy"].Proto != "ssh" {
		t.Fatalf("proto = %q, want ssh", cfg.Plugins["legacy"].Proto)
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestValidateChecksProto(t *testing.T) {
	tests := []struct {
		name   string
		plugin RawPlugin
	}{
		{name: "unknown value", plugin: RawPlugin{GitHub: "a/b", Proto: "ftp"}},
		{name: "without github or gist", plugin: RawPlugin{Remote: "https://example.test/x", Proto: "ssh"}},
		{name: "with plain git source", plugin: RawPlugin{Git: "https://example.test/x.git", Proto: "ssh"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(Config{Plugins: map[string]RawPlugin{"bad": test.plugin}}); err == nil {
				t.Fatalf("proto %+v was accepted", test.plugin)
			}
		})
	}
	for _, proto := range []string{"git", "https", "ssh"} {
		cfg := Config{Plugins: map[string]RawPlugin{"good": {Gist: "abc123", Proto: proto}}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("proto %q rejected: %v", proto, err)
		}
	}
}

func TestValidateAllowsOptionalLocalPluginsOnly(t *testing.T) {
	valid := Config{Plugins: map[string]RawPlugin{"local": {Local: "/missing/plugin", Optional: true}}}
	if err := Validate(valid); err != nil {
		t.Fatalf("optional local plugin was rejected: %v", err)
	}
	invalid := Config{Plugins: map[string]RawPlugin{"remote": {Remote: "https://example.test/plugin.zsh", Optional: true}}}
	if err := Validate(invalid); err == nil || !strings.Contains(err.Error(), "optional") {
		t.Fatalf("optional remote plugin error = %v", err)
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

func TestValidateRejectsInlinePluginFields(t *testing.T) {
	// An inline plugin carries only text, hooks, and profiles.
	fields := map[string]RawPlugin{
		"proto":  {Inline: "echo hi", Proto: "ssh"},
		"rev":    {Inline: "echo hi", Rev: "v1.0.0"},
		"branch": {Inline: "echo hi", Branch: "main"},
		"tag":    {Inline: "echo hi", Tag: "v1.0.0"},
		"dir":    {Inline: "echo hi", Dir: "plugins/demo"},
		"file":   {Inline: "echo hi", File: "demo.plugin.zsh"},
		"use":    {Inline: "echo hi", Use: []string{"*.zsh"}},
		"apply":  {Inline: "echo hi", Apply: []string{"source"}},
	}
	for field, plugin := range fields {
		cfg := Config{Plugins: map[string]RawPlugin{"demo": plugin}}
		err := Validate(cfg)
		if err == nil {
			t.Errorf("inline plugin with %s was accepted", field)
			continue
		}
		if !strings.Contains(err.Error(), field) {
			t.Errorf("inline plugin with %s: error = %q", field, err)
		}
	}
	valid := Config{Plugins: map[string]RawPlugin{"demo": {Inline: "echo hi", Hooks: map[string]string{"pre": "echo pre"}}}}
	if err := Validate(valid); err != nil {
		t.Fatalf("valid inline plugin was rejected: %v", err)
	}
}

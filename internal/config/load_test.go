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

func TestLoadAndValidateEnvironmentAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[env]\nZSH_THEME = \"robbyrussell\"\nplugins = \"(git npm macos)\"\n\n[plugins.demo]\ninline = \"echo demo\"\n"
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
	if cfg.Env["ZSH_THEME"] != "robbyrussell" || cfg.Env["plugins"] != "(git npm macos)" {
		t.Fatalf("environment = %v", cfg.Env)
	}
}

func TestValidateRejectsInvalidEnvironmentName(t *testing.T) {
	cfg := Config{Env: map[string]string{"NOT-VALID": "value"}, Plugins: map[string]RawPlugin{"demo": {Inline: "echo demo"}}}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "environment variable") {
		t.Fatalf("invalid environment name error = %v", err)
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

func TestLoadAndValidateMultiForgeSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[plugins.glab]\ngitlab = \"owner/repo\"\n\n[plugins.bb]\nbitbucket = \"team/project\"\nproto = \"ssh\"\n\n[plugins.cb]\ncodeberg = \"owner/repo\"\n"
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
	if cfg.Plugins["glab"].GitLab != "owner/repo" || cfg.Plugins["bb"].Bitbucket != "team/project" || cfg.Plugins["bb"].Proto != "ssh" || cfg.Plugins["cb"].Codeberg != "owner/repo" {
		t.Fatalf("multi-forge plugins = %+v", cfg.Plugins)
	}
	// A forge key counts as one source, so two of them must be rejected.
	multiple := Config{Plugins: map[string]RawPlugin{"bad": {GitHub: "a/b", GitLab: "c/d"}}}
	if err := Validate(multiple); err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Fatalf("two forge sources error = %v", err)
	}
	// proto selects the protocol for every forge host.
	for _, plugin := range []RawPlugin{
		{GitHub: "a/b", Proto: "ssh"},
		{Gist: "abc123", Proto: "git"},
		{GitLab: "a/b", Proto: "ssh"},
		{Bitbucket: "a/b", Proto: "https"},
		{Codeberg: "a/b", Proto: "git"},
	} {
		if err := Validate(Config{Plugins: map[string]RawPlugin{"good": plugin}}); err != nil {
			t.Fatalf("forge source %+v with proto was rejected: %v", plugin, err)
		}
	}
	// proto still requires a forge host: a plain git URL cannot set it.
	if err := Validate(Config{Plugins: map[string]RawPlugin{"bad": {Git: "https://example.test/x.git", Proto: "ssh"}}}); err == nil {
		t.Fatal("proto on a plain git source was accepted")
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
		"proto":     {Inline: "echo hi", Proto: "ssh"},
		"rev":       {Inline: "echo hi", Rev: "v1.0.0"},
		"branch":    {Inline: "echo hi", Branch: "main"},
		"tag":       {Inline: "echo hi", Tag: "v1.0.0"},
		"dir":       {Inline: "echo hi", Dir: "plugins/demo"},
		"file":      {Inline: "echo hi", File: "demo.plugin.zsh"},
		"use":       {Inline: "echo hi", Use: []string{"*.zsh"}},
		"apply":     {Inline: "echo hi", Apply: []string{"source"}},
		"cloneopts": {Inline: "echo hi", CloneOpts: []string{"--no-tags"}},
		"depth":     {Inline: "echo hi", Depth: intPtr(2)},
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

func TestLoadAndValidateCloneOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[plugins.p10k]\ngithub = \"romkatv/powerlevel10k\"\ncloneopts = [\"--single-branch\", \"--filter=blob:none\"]\ndepth = 0\n"
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
	plugin := cfg.Plugins["p10k"]
	if len(plugin.CloneOpts) != 2 || plugin.CloneOpts[0] != "--single-branch" || plugin.CloneOpts[1] != "--filter=blob:none" {
		t.Fatalf("cloneopts = %v", plugin.CloneOpts)
	}
	if plugin.Depth == nil || *plugin.Depth != 0 {
		t.Fatalf("depth = %v, want 0", plugin.Depth)
	}
}

func TestValidateChecksCloneOptions(t *testing.T) {
	tests := []struct {
		name   string
		plugin RawPlugin
	}{
		{name: "cloneopts on remote", plugin: RawPlugin{Remote: "https://example.test/plugin.zsh", CloneOpts: []string{"--single-branch"}}},
		{name: "cloneopts on local", plugin: RawPlugin{Local: "/plugins", CloneOpts: []string{"--single-branch"}}},
		{name: "empty cloneopt", plugin: RawPlugin{GitHub: "a/b", CloneOpts: []string{""}}},
		{name: "negative depth", plugin: RawPlugin{GitHub: "a/b", Depth: intPtr(-1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{Plugins: map[string]RawPlugin{"bad": test.plugin}}
			if err := Validate(cfg); err == nil {
				t.Fatalf("plugin %+v was accepted", test.plugin)
			}
		})
	}
	depth := 2
	git := Config{Plugins: map[string]RawPlugin{"good": {Git: "https://example.test/repo.git", CloneOpts: []string{"--no-tags"}, Depth: &depth}}}
	if err := Validate(git); err != nil {
		t.Fatalf("git plugin with cloneopts and depth was rejected: %v", err)
	}
	for _, plugin := range []RawPlugin{{GitHub: "a/b", Depth: intPtr(0)}, {Gist: "abc123", Depth: intPtr(3)}} {
		if err := Validate(Config{Plugins: map[string]RawPlugin{"good": plugin}}); err != nil {
			t.Fatalf("git plugin %+v was rejected: %v", plugin, err)
		}
	}
}

func intPtr(value int) *int { return &value }

func TestBuildRequiresADirectorySource(t *testing.T) {
	tests := []struct {
		name   string
		plugin RawPlugin
	}{
		{name: "inline", plugin: RawPlugin{Inline: "echo hi", Build: []string{"make"}}},
		{name: "remote", plugin: RawPlugin{Remote: "https://example.com/plugin.zsh", Build: []string{"make"}}},
		{name: "empty entry", plugin: RawPlugin{Local: "~/demo", Build: []string{""}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{Plugins: map[string]RawPlugin{"demo": test.plugin}}
			if err := Validate(cfg); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

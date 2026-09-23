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

func TestAddRejectsPluginNamesThatWouldNestTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, "my.plugin", RawPlugin{Inline: "echo hi"}); err == nil {
		t.Fatal("dotted plugin name was accepted")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("failed add changed the config: %s", contents)
	}
}

func TestAddPluginWithHooksRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin := RawPlugin{Inline: "echo hi", Hooks: map[string]string{"pre": "echo pre", "post": "echo post"}}
	if err := Add(path, "hooked", plugin); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The hooks subtable must hang off the plugin's own section, or it cannot reload.
	if !strings.Contains(string(contents), "[plugins.hooked.hooks]") {
		t.Fatalf("hooks section missing from %s", contents)
	}
	if strings.Contains(string(contents), "[plugins.pre.hooks]") || strings.Contains(string(contents), "[plugins.post.hooks]") {
		t.Fatalf("hooks section used the hook name: %s", contents)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, ok := cfg.Plugins["hooked"]
	if !ok {
		t.Fatalf("plugin %q missing after reload: %#v", "hooked", cfg.Plugins)
	}
	if loaded.Inline != "echo hi" {
		t.Fatalf("inline = %q, want %q", loaded.Inline, "echo hi")
	}
	if loaded.Hooks["pre"] != "echo pre" || loaded.Hooks["post"] != "echo post" {
		t.Fatalf("hooks = %#v, want pre and post", loaded.Hooks)
	}
	if len(cfg.PluginOrder) != 1 || cfg.PluginOrder[0] != "hooked" {
		t.Fatalf("plugin order = %v, want [hooked]", cfg.PluginOrder)
	}
}

func TestAddRejectsTOMLHostilePluginNames(t *testing.T) {
	for _, name := range []string{"", "two words", `quo"te`, "bracket]", "hash#", "equals=", "line\nbreak"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Add(path, name, RawPlugin{Inline: "echo hi"}); err == nil {
			t.Errorf("plugin name %q was accepted", name)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(contents), "plugins") {
			t.Errorf("plugin name %q wrote a section: %s", name, contents)
		}
	}
}

func TestAddRejectsDuplicateNestedPluginName(t *testing.T) {
	// plugins.fzf.inline decodes as plugin "fzf", which a literal section search never matches.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\nplugins.fzf.inline = \"echo existing\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, "fzf", RawPlugin{Inline: "echo new"}); err == nil {
		t.Fatal("duplicate dotted plugin name was accepted")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("failed add changed the config: %s", contents)
	}
}

func TestAddRefusesToWriteConfigItCannotReload(t *testing.T) {
	// An edit must reload and validate before it replaces the file, or corruption goes unnoticed.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\n[plugins.broken]\nrev = \"main\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, "good", RawPlugin{Inline: "echo hi"}); err == nil {
		t.Fatal("add accepted an edit that leaves the config invalid")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("failed add changed the config: %s", contents)
	}
}

func TestAddWritesProtoRatherThanProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, "private", RawPlugin{GitHub: "rubiin/repository", Proto: "ssh"}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "proto = \"ssh\"") || strings.Contains(string(contents), "protocol") {
		t.Fatalf("config = %s, want the proto spelling", contents)
	}
}

func TestAddWritesCloneOptionsAndDepth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, "p10k", RawPlugin{GitHub: "romkatv/powerlevel10k", CloneOpts: []string{"--single-branch", "--filter=blob:none"}, Depth: intPtr(0)}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "cloneopts = [\"--single-branch\", \"--filter=blob:none\"]") {
		t.Fatalf("add did not write cloneopts: %s", contents)
	}
	if !strings.Contains(string(contents), "depth = 0") {
		t.Fatalf("add did not write depth: %s", contents)
	}
}

func TestAddWritesIgnoreGlobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, "demo", RawPlugin{GitHub: "rubiin/demo", Use: []string{"**/*.zsh"}, Ignore: []string{"**/test*", "**/tests/*"}}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "ignore = [\"**/test*\", \"**/tests/*\"]") {
		t.Fatalf("add did not write ignore: %s", contents)
	}
}

func TestAddWritesMultiForgeFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, "glab", RawPlugin{GitLab: "owner/glab", Proto: "ssh"}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "gitlab = \"owner/glab\"") || !strings.Contains(string(contents), "proto = \"ssh\"") {
		t.Fatalf("add did not write the gitlab source: %s", contents)
	}
}

func TestRemoveDeletesDottedKeyPlugin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\nplugins.fzf.inline = \"echo fzf\"\n\n[plugins.kept]\ninline = \"echo kept\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "fzf"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "fzf") {
		t.Fatalf("dotted key plugin survived remove: %s", contents)
	}
	if !strings.Contains(string(contents), "[plugins.kept]") {
		t.Fatalf("unrelated plugin lost: %s", contents)
	}
}

func TestRemoveDeletesQuotedPluginName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\n[plugins.\"my.plugin\"]\ninline = \"echo mine\"\n\n[plugins.kept]\ninline = \"echo kept\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "my.plugin"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "my.plugin") {
		t.Fatalf("quoted plugin survived remove: %s", contents)
	}
	if !strings.Contains(string(contents), "[plugins.kept]") {
		t.Fatalf("unrelated plugin lost: %s", contents)
	}
}

func TestRemoveLeavesDottedKeysThatBelongToAnotherTable(t *testing.T) {
	// A dotted key after a table header belongs to that table, so it is not a top-level plugin.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\n[plugins.owner]\ninline = \"echo owner\"\nplugins.guest.inline = \"echo guest\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "guest"); err == nil {
		t.Fatal("remove accepted a plugin that is not declared at the top level")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("failed remove changed the config: %s", contents)
	}
}

func TestRemoveDeletesMultiLineDottedKeyValue(t *testing.T) {
	// A dotted-key value split across lines must be dropped whole, not left as orphaned lines.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\nplugins.foo.apply = [\n  \"a\",\n  \"b\",\n]\nplugins.foo.inline = \"echo foo\"\n\n[plugins.kept]\ninline = \"echo kept\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "foo"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "foo") {
		t.Fatalf("remove left the multi-line plugin behind: %s", contents)
	}
	if !strings.Contains(string(contents), "[plugins.kept]") {
		t.Fatalf("remove lost the unrelated plugin: %s", contents)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("removed config no longer decodes: %v\n%s", err, contents)
	}
}

func TestRemoveIgnoresBracketsInsideQuotedValues(t *testing.T) {
	// Array elements containing "[" and "]" must not close the multi-line value early.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\nplugins.foo.use = [\n  \"a]b[\",\n  \"c\",\n]\n\n[plugins.kept]\ninline = \"echo kept\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "foo"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "foo") {
		t.Fatalf("remove cut the multi-line value short: %s", contents)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("removed config no longer decodes: %v\n%s", err, contents)
	}
}

func TestRemoveRefusesToWriteACorruptConfig(t *testing.T) {
	// A multi-line value the line scanner cannot track (here a """ string) must fail the
	// round-trip validation instead of silently writing orphaned lines to disk.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\nplugins.gone.inline = \"\"\"\necho first\necho second\"\"\"\n\n[plugins.kept]\ninline = \"echo kept\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "gone"); err == nil {
		t.Fatal("remove wrote a config that no longer decodes")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("failed remove changed the config: %s", contents)
	}
}

func TestRemoveKeepsFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("shell = \"zsh\"\n\n[plugins.gone]\ninline = \"echo gone\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "gone"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
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

func TestRemoveDeletesInlineTablePlugin(t *testing.T) {
	// [plugins] entry declared as an inline table must be removable, and the
	// surrounding comments and sibling plugins must survive.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\n[plugins]\n# keep this comment\ndemo = { github = \"rubiin/demo\", use = [\"*.zsh\"] }\nkept = { github = \"rubiin/kept\" }\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "demo"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "demo") {
		t.Fatalf("inline-table plugin survived remove: %s", contents)
	}
	if !strings.Contains(string(contents), "kept = { github = \"rubiin/kept\" }") || !strings.Contains(string(contents), "# keep this comment") {
		t.Fatalf("remove lost unrelated content: %s", contents)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("removed config no longer decodes: %v\n%s", err, contents)
	}
}

func TestRemoveDeletesQuotedInlineTablePlugin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\n[plugins]\n\"my.demo\" = { github = \"rubiin/demo\" }\nkept = { github = \"rubiin/kept\" }\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "my.demo"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "my.demo") {
		t.Fatalf("quoted inline-table plugin survived remove: %s", contents)
	}
	if !strings.Contains(string(contents), "kept = { github = \"rubiin/kept\" }") {
		t.Fatalf("remove lost the unrelated inline-table plugin: %s", contents)
	}
}

func TestRemoveIgnoresTrailingCommentsOnTableHeaders(t *testing.T) {
	// A trailing comment on a table header must not hide the plugin from remove.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\n# a leading comment\n[plugins.gone] # remove this plugin\ninline = \"echo gone\"\n\n[plugins.kept] # keep this one\ninline = \"echo kept\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "gone"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "plugins.gone") {
		t.Fatalf("commented header plugin survived remove: %s", contents)
	}
	if !strings.Contains(string(contents), "[plugins.kept] # keep this one") || !strings.Contains(string(contents), "# a leading comment") {
		t.Fatalf("remove lost comments or the unrelated plugin: %s", contents)
	}
}

func TestRemoveRefusesToEditAnUnparsableConfig(t *testing.T) {
	// An unmatched [ in a multi-line value must not make remove drop the table
	// header that follows it (and the rest of the config): an unparsable config
	// is refused and left untouched.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n\nplugins.gone.apply = [\n  \"a\",\n  \"b\",\n\n# comment\n[plugins.kept]\ninline = \"echo kept\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path, "gone"); err == nil {
		t.Fatal("remove edited a config it cannot parse")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("failed remove changed the config: %s", contents)
	}
}

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRootCommands(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"bash\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	root := NewRoot()
	if root.Use != "shelf" {
		t.Fatalf("root use = %q", root.Use)
	}
	commands := map[string]bool{}
	for _, command := range root.Commands() {
		commands[command.Name()] = true
	}
	for _, name := range []string{"init", "lock", "source", "add", "edit", "remove", "completions", "version"} {
		if !commands[name] {
			t.Errorf("root command %q is missing", name)
		}
		command, _, err := root.Find([]string{name})
		if err != nil || command.Short == "" {
			t.Errorf("root command %q has no description", name)
		}
	}

	root.SetArgs([]string{"--verbose", "--profile", "work", "source"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatalf("global flags before command failed: %v", err)
	}
	if !verbose || profile != "work" {
		t.Fatalf("global flags were not captured: verbose=%v profile=%q", verbose, profile)
	}
}

func TestShelfPrefixesDirectoryFlagsAndEnvironment(t *testing.T) {
	t.Setenv("SHELF_CONFIG_DIR", "/env/config")
	t.Setenv("SHELF_DATA_DIR", "/env/data")
	t.Setenv("SHELF_CONFIG_FILE", "/env/config.toml")
	t.Setenv("SHELF_PROFILE", "work")

	root := NewRoot()
	for _, name := range []string{"config-dir", "data-dir", "config-file"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("missing prefixed flag --%s", name)
		}
	}
	if got := root.PersistentFlags().Lookup("config-dir").DefValue; got != "/env/config" {
		t.Errorf("config dir default = %q", got)
	}
	if got := root.PersistentFlags().Lookup("data-dir").DefValue; got != "/env/data" {
		t.Errorf("data dir default = %q", got)
	}
	if got := root.PersistentFlags().Lookup("config-file").DefValue; got != "/env/config.toml" {
		t.Errorf("config file default = %q", got)
	}
	if profile != "work" {
		t.Errorf("profile env default = %q", profile)
	}
}

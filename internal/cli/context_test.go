package cli

import (
	"path/filepath"
	"testing"
)

func TestResolvePathsUsesXDGDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	home := t.TempDir()
	paths, err := ResolvePaths(home, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigDirectory != filepath.Join(home, ".config", "shelf") {
		t.Fatalf("config directory = %q", paths.ConfigDirectory)
	}
	if paths.DataDirectory != filepath.Join(home, ".local", "share", "shelf") {
		t.Fatalf("data directory = %q", paths.DataDirectory)
	}
	if paths.ConfigFile != filepath.Join(paths.ConfigDirectory, "config.toml") {
		t.Fatalf("config file = %q", paths.ConfigFile)
	}
}

func TestResolvePathsHonorsExplicitOverrides(t *testing.T) {
	paths, err := ResolvePaths("/home/tester", "/tmp/config", "/tmp/data", "/tmp/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigDirectory != "/tmp/config" || paths.DataDirectory != "/tmp/data" || paths.ConfigFile != "/tmp/config.toml" {
		t.Fatalf("unexpected paths: %+v", paths)
	}
}

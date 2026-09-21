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
	if paths.ConfigFile != filepath.Join(paths.ConfigDirectory, "plugins.toml") {
		t.Fatalf("config file = %q", paths.ConfigFile)
	}
	if paths.LockFile("") != filepath.Join(paths.DataDirectory, "plugins.lock") {
		t.Fatalf("lock file = %q", paths.LockFile(""))
	}
}

func TestResolvePathsDerivesConfigDirectoryFromConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	paths, err := ResolvePaths("/home/tester", "", "/tmp/data", "/tmp/plugins/custom.toml")
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigDirectory != "/tmp/plugins" {
		t.Fatalf("config directory = %q, want the config file's parent", paths.ConfigDirectory)
	}
	if paths.ConfigFile != "/tmp/plugins/custom.toml" {
		t.Fatalf("config file = %q", paths.ConfigFile)
	}
	if paths.DataDirectory != "/tmp/data" {
		t.Fatalf("data directory = %q", paths.DataDirectory)
	}
}

func TestResolvePathsHonorsExplicitOverrides(t *testing.T) {
	paths, err := ResolvePaths("/home/tester", "/tmp/config", "/tmp/data", "/tmp/custom.toml")
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigDirectory != "/tmp/config" || paths.DataDirectory != "/tmp/data" || paths.ConfigFile != "/tmp/custom.toml" {
		t.Fatalf("unexpected paths: %+v", paths)
	}
}

func TestLockFileCarriesTheProfileName(t *testing.T) {
	paths := Paths{ConfigDirectory: "/tmp/config", DataDirectory: "/tmp/data", ConfigFile: "/tmp/config/plugins.toml"}
	if got := paths.LockFile(""); got != filepath.Join("/tmp/data", "plugins.lock") {
		t.Fatalf("lock file = %q", got)
	}
	if got := paths.LockFile("work"); got != filepath.Join("/tmp/data", "plugins.work.lock") {
		t.Fatalf("profile lock file = %q", got)
	}
	if got := paths.RevisionLockFile(""); got != filepath.Join("/tmp/config", "plugins.lock") {
		t.Fatalf("revision lock file = %q", got)
	}
	if got := paths.RevisionLockFile("work"); got != filepath.Join("/tmp/config", "plugins.work.lock") {
		t.Fatalf("profile revision lock file = %q", got)
	}
}

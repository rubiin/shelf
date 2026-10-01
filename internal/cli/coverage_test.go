package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shelf/internal/lock"
	"shelf/internal/selfupdate"
)

// TestSelfUpdateCommandInvokesConfirm exercises the inline confirmation hook the
// command hands to the updater, which is otherwise only reached behind a TTY.
func TestSelfUpdateCommandInvokesConfirm(t *testing.T) {
	originalNonInteractive := nonInteractive
	nonInteractive = false
	t.Cleanup(func() { nonInteractive = originalNonInteractive })

	original := runUpdate
	invoked := false
	runUpdate = func(_ context.Context, options selfupdate.Options) (selfupdate.Result, error) {
		invoked = true
		approved, err := options.Confirm("1.2.3")
		if err != nil {
			return selfupdate.Result{}, err
		}
		if approved {
			t.Error("confirmation reported approval without a terminal")
		}
		return selfupdate.Result{Updated: false, Next: "1.2.3"}, nil
	}
	t.Cleanup(func() { runUpdate = original })

	var diagnostics bytes.Buffer
	if err := Execute([]string{"self-update"}, &bytes.Buffer{}, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if !invoked {
		t.Fatal("self-update did not call the confirmation hook")
	}
	// The spinner is silent off a terminal, so pausing it around the prompt
	// leaves stderr byte-identical.
	if diagnostics.Len() != 0 {
		t.Fatalf("self-update wrote spinner output to a non-terminal: %q", diagnostics.String())
	}
}

// TestLaunchSurfacesChdirError calls the real launch with a missing directory,
// which returns before the process-replacing exec.
func TestLaunchSurfacesChdirError(t *testing.T) {
	if err := launch(filepath.Join(t.TempDir(), "missing"), []string{"true"}); err == nil {
		t.Fatal("launch accepted a missing directory")
	}
}

func TestFindCachePathsSurfacesDownloadStatError(t *testing.T) {
	// A self-referential downloads symlink makes the stat fail with ELOOP, which
	// is neither a missing path nor a clean directory.
	directory := t.TempDir()
	if err := os.Symlink("downloads", filepath.Join(directory, "downloads")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := findCachePaths(directory); err == nil {
		t.Fatal("findCachePaths ignored a non-NotExist stat error")
	}
}

func TestStatusJSONSurfacesWriteError(t *testing.T) {
	withCleanProfile(t)
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.first]\ninline = \"echo first\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"status", "--json"}, errWriter{}, &bytes.Buffer{}); err == nil {
		t.Fatal("status --json swallowed a write error")
	}
}

func TestFindCachePathsSurfacesGlobError(t *testing.T) {
	// An unmatched "[" in the data directory makes the zcompdump glob malformed.
	directory := filepath.Join(t.TempDir(), "data[")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := findCachePaths(directory); err == nil {
		t.Fatal("findCachePaths accepted a malformed zcompdump glob")
	}
}

func TestCdPluginSurfacesConfigAndShellErrors(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.demo]\nlocal = \"/tmp/demo\"\n")
	directory := t.TempDir()
	if err := lock.Write(paths.LockFile(""), lock.LockedConfig{Shell: "bash", Plugins: []lock.LockedPlugin{{Name: "demo", Directory: directory}}}); err != nil {
		t.Fatal(err)
	}
	// Undecodable TOML fails the config load before the shell is resolved.
	if err := os.WriteFile(paths.ConfigFile, []byte("shell = \"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cdPlugin(paths, "demo", nil); err == nil {
		t.Fatal("cdPlugin accepted an undecodable config")
	}
	// A config with no shell and an invalid SHELF_SHELL fails shell resolution.
	if err := os.WriteFile(paths.ConfigFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_SHELL", "fish")
	if err := cdPlugin(paths, "demo", nil); err == nil || !strings.Contains(err.Error(), "SHELF_SHELL") {
		t.Fatalf("cdPlugin err = %v, want an unsupported SHELF_SHELL error", err)
	}
}

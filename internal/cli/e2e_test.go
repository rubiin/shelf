package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"shelf/internal/config"
	"shelf/internal/filelock"
	"shelf/internal/lock"
	"shelf/internal/selfupdate"
	"shelf/internal/source"
	"shelf/internal/tui"
)

func TestInlineLockAndSource(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Execute([]string{"source"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "echo testing") {
		t.Fatalf("source output = %q", output.String())
	}
}

func TestSourceRendersZcompileGuardForZshOnly(t *testing.T) {
	directory := t.TempDir()
	pluginDirectory := filepath.Join(directory, "plugin")
	if err := os.MkdirAll(pluginDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	zshFile := filepath.Join(pluginDirectory, "demo.plugin.zsh")
	if err := os.WriteFile(zshFile, []byte("echo compiled-e2e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bashFile := filepath.Join(pluginDirectory, "demo.plugin.bash")
	if err := os.WriteFile(bashFile, []byte("echo bash-e2e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		shell       string
		use         string
		mustContain string
		mustAbsent  string
	}{
		{name: "zsh compiles before sourcing", shell: "zsh", use: "demo.plugin.zsh", mustContain: `]] && zcompile "` + zshFile, mustAbsent: ""},
		{name: "bash no-op", shell: "bash", use: "demo.plugin.bash", mustContain: `source "` + bashFile, mustAbsent: "zcompile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configFile := filepath.Join(directory, "config-"+test.shell+".toml")
			config := fmt.Sprintf("shell = %q\n\n[plugins.demo]\nlocal = %q\napply = [\"zcompile\"]\nuse = [%q]\n", test.shell, pluginDirectory, test.use)
			if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SHELF_CONFIG_FILE", configFile)
			t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data-"+test.shell))
			if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := Execute([]string{"source"}, &output, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.mustContain) {
				t.Fatalf("source output missing %q:\n%s", test.mustContain, output.String())
			}
			if test.mustAbsent != "" && strings.Contains(output.String(), test.mustAbsent) {
				t.Fatalf("source output contains %q:\n%s", test.mustAbsent, output.String())
			}
		})
	}
}

func TestSourceRendersDeferredSourceForZshOnly(t *testing.T) {
	directory := t.TempDir()
	pluginDirectory := filepath.Join(directory, "plugin")
	if err := os.MkdirAll(pluginDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	zshFile := filepath.Join(pluginDirectory, "demo.plugin.zsh")
	if err := os.WriteFile(zshFile, []byte("echo deferred-e2e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bashFile := filepath.Join(pluginDirectory, "demo.plugin.bash")
	if err := os.WriteFile(bashFile, []byte("echo bash-e2e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		shell       string
		use         string
		mustContain string
		mustAbsent  string
	}{
		{name: "zsh defers the source", shell: "zsh", use: "demo.plugin.zsh", mustContain: `_shelf_defer source "` + zshFile, mustAbsent: ""},
		{name: "bash loads immediately", shell: "bash", use: "demo.plugin.bash", mustContain: `source "` + bashFile, mustAbsent: "_shelf_defer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configFile := filepath.Join(directory, "config-defer-"+test.shell+".toml")
			config := fmt.Sprintf("shell = %q\n\n[plugins.demo]\nlocal = %q\napply = [\"defer\"]\nuse = [%q]\n", test.shell, pluginDirectory, test.use)
			if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SHELF_CONFIG_FILE", configFile)
			t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data-defer-"+test.shell))
			if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := Execute([]string{"source"}, &output, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.mustContain) {
				t.Fatalf("source output missing %q:\n%s", test.mustContain, output.String())
			}
			if test.mustAbsent != "" && strings.Contains(output.String(), test.mustAbsent) {
				t.Fatalf("source output contains %q:\n%s", test.mustAbsent, output.String())
			}
		})
	}
}

func TestSourceLeavesNoStrayLockFile(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Execute([]string{"source"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "echo testing") {
		t.Fatalf("source output = %q", output.String())
	}
	stray := filepath.Join(configDir, ".lock")
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("source left a stray lock file at %s: %v", stray, err)
	}
}

// TestUpdateEmitsSourceAndPersistsLockfile guards the fix where bare update left the old
// lock on disk, so the next fast-path source restored the pre-update revisions.
func TestUpdateEmitsSourceAndPersistsLockfile(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo updated\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var output bytes.Buffer
	if err := Execute([]string{"update"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "echo updated") {
		t.Fatalf("update output = %q", output.String())
	}
	if _, err := os.Stat(filepath.Join(directory, "data", "plugins.lock")); err != nil {
		t.Fatalf("update did not persist the lock file: %v", err)
	}
}

func TestUpdateLockWritesLockfileWithoutSource(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo updated\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var output bytes.Buffer
	if err := Execute([]string{"update", "--lock"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("update --lock output = %q", output.String())
	}
	if _, err := os.Stat(filepath.Join(directory, "data", "plugins.lock")); err != nil {
		t.Fatalf("update --lock did not write lock file: %v", err)
	}
}

func TestPathPrintsResolvedPaths(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	configFile := filepath.Join(configDir, "config.toml")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_DATA_DIR", dataDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)

	var output bytes.Buffer
	if err := Execute([]string{"path"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	want := "config_dir=" + configDir + "\n" +
		"data_dir=" + dataDir + "\n" +
		"config_file=" + configFile + "\n" +
		"lock_file=" + filepath.Join(dataDir, "plugins.lock") + "\n"
	if output.String() != want {
		t.Fatalf("path output = %q, want %q", output.String(), want)
	}
}

func TestReloadPrintsExecForTheConfiguredShell(t *testing.T) {
	for _, test := range []struct {
		name  string
		shell string
		want  string
		env   string
	}{
		{name: "zsh", shell: "zsh", want: "exec zsh\n"},
		{name: "bash", shell: "bash", want: "exec bash\n"},
		// A config without a shell falls back to SHELF_SHELL.
		{name: "bash via env", shell: "", want: "exec bash\n", env: "bash"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			configDir := filepath.Join(directory, "config")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatal(err)
			}
			configFile := filepath.Join(configDir, "config.toml")
			config := ""
			if test.shell != "" {
				config = "shell = \"" + test.shell + "\"\n"
			}
			if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SHELF_CONFIG_DIR", configDir)
			t.Setenv("SHELF_CONFIG_FILE", configFile)
			t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
			if test.env != "" {
				t.Setenv("SHELF_SHELL", test.env)
			} else {
				t.Setenv("SHELF_SHELL", "")
			}

			var output bytes.Buffer
			if err := Execute([]string{"reload"}, &output, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("reload output = %q, want %q", output.String(), test.want)
			}
		})
	}
}

func TestReloadRejectsAnUnsupportedShellOverride(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_SHELL", "csh")

	var output bytes.Buffer
	var diagnostics bytes.Buffer
	if err := Execute([]string{"reload"}, &output, &diagnostics); err == nil {
		t.Fatal("reload succeeded for an unsupported SHELF_SHELL")
	}
	if output.String() != "" {
		t.Fatalf("reload stdout = %q, want empty", output.String())
	}
	if !strings.Contains(diagnostics.String(), `unsupported shell "csh" in SHELF_SHELL`) {
		t.Fatalf("reload diagnostics = %q, want unsupported shell error", diagnostics.String())
	}
}

func TestReloadRejectsAnInvalidConfigShell(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"fish\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var output bytes.Buffer
	var diagnostics bytes.Buffer
	if err := Execute([]string{"reload"}, &output, &diagnostics); err == nil || !strings.Contains(diagnostics.String(), "unsupported shell") {
		t.Fatalf("reload err = %v, diagnostics = %q, want unsupported shell error", err, diagnostics.String())
	}
	if output.Len() != 0 {
		t.Fatalf("reload stdout = %q, want empty", output.String())
	}
}

func TestLockStoresLockfileInDataDirectory(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "plugins.lock")); err != nil {
		t.Fatalf("lock file missing in data directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "plugins.lock")); err != nil {
		t.Fatalf("revision lock file missing in config directory: %v", err)
	}
}

func TestLockUsesAProfileSpecificLockFile(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.work]\nprofiles = [\"work\"]\ninline = \"echo work\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)
	t.Setenv("SHELF_PROFILE", "work")

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	profileLock := filepath.Join(dataDir, "plugins.work.lock")
	contents, err := os.ReadFile(profileLock)
	if err != nil {
		t.Fatalf("profile lock file missing: %v", err)
	}
	if !strings.Contains(string(contents), "name = \"work\"") || !strings.Contains(string(contents), "profile = \"work\"") {
		t.Fatalf("profile lock file = %s", contents)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "plugins.lock")); !os.IsNotExist(err) {
		t.Fatalf("unprofiled lock file was written: %v", err)
	}
}

func TestProfileExcludesPluginsWithoutASelectedProfile(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.always]\ninline = \"echo always\"\n\n[plugins.work]\nprofiles = [\"work\"]\ninline = \"echo work\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dataDir, "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "name = \"work\"") {
		t.Fatalf("plugin with profiles was locked without a selected profile: %s", contents)
	}
	if !strings.Contains(string(contents), "name = \"always\"") {
		t.Fatalf("plugin without profiles was skipped: %s", contents)
	}
}

func TestLockWarnsForUnmatchedProfile(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.always]\ninline = \"echo always\"\n\n[plugins.work]\nprofiles = [\"work\"]\ninline = \"echo work\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_PROFILE", "typo")

	var stderr bytes.Buffer
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "profile \"typo\" matches no plugins") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRemoveDeletesDottedKeyPlugin(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\nplugins.fzf.inline = \"echo fzf\"\n\n[plugins.kept]\ninline = \"echo kept\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var output bytes.Buffer
	if err := Execute([]string{"remove", "fzf"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "✓ removed: fzf") {
		t.Fatalf("remove output = %q, want ✓ removed: fzf", output.String())
	}
	contents, err := os.ReadFile(configFile)
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

func TestAddWritesProtoField(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var output bytes.Buffer
	if err := Execute([]string{"add", "private", "--github", "rubiin/repository", "--proto", "ssh"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "✓ added: private") {
		t.Fatalf("add output = %q, want ✓ added: private", output.String())
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "proto = \"ssh\"") {
		t.Fatalf("add did not write the proto field: %s", contents)
	}
}

func TestAddWritesGitLabField(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"add", "glab", "--gitlab", "owner/glab", "--proto", "ssh"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "gitlab = \"owner/glab\"") || !strings.Contains(string(contents), "proto = \"ssh\"") {
		t.Fatalf("add did not write the gitlab source: %s", contents)
	}
}

func TestAddWritesCloneOptionsAndDepth(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"add", "p10k", "--github", "romkatv/powerlevel10k", "--cloneopts=--single-branch,--no-tags", "--depth", "0"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "cloneopts = [\"--single-branch\", \"--no-tags\"]") {
		t.Fatalf("add did not write cloneopts: %s", contents)
	}
	if !strings.Contains(string(contents), "depth = 0") {
		t.Fatalf("add did not write depth: %s", contents)
	}
}

func TestAddRejectsCloneOptionsOnInlinePlugin(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"add", "demo", "--inline", "echo hi", "--depth", "1"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("add accepted depth on an inline plugin")
	}
}

func TestListWorksWithoutLockFile(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.first]\ninline = \"echo first\"\n\n[plugins.second]\ninline = \"echo second\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var output bytes.Buffer
	if err := Execute([]string{"list"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "first\nsecond\n" {
		t.Fatalf("list output = %q", output.String())
	}
}

func TestListReflectsConfigNotStaleLock(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.kept]\ninline = \"echo kept\"\n\n[plugins.added]\ninline = \"echo added\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	staleLock := "config_fingerprint = \"stale\"\nshell = \"zsh\"\n\n[[plugins]]\n  name = \"removed\"\n  directory = \"/tmp/removed\"\n  files = []\n"
	if err := os.MkdirAll(filepath.Join(directory, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "data", "plugins.lock"), []byte(staleLock), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var output bytes.Buffer
	if err := Execute([]string{"list"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "kept\nadded\n" {
		t.Fatalf("list output = %q, want configured plugins only", output.String())
	}
}

func TestRemoveInteractiveWorksWithoutLockFile(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.alpha]\ninline = \"echo a\"\n\n[plugins.beta]\ninline = \"echo b\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	original := interactiveSelect
	interactiveSelect = func(options []string, _ io.Writer) ([]string, error) {
		if len(options) != 2 || options[0] != "alpha" || options[1] != "beta" {
			t.Errorf("picker options = %v, want alpha, beta from config", options)
		}
		return []string{"alpha"}, nil
	}
	t.Cleanup(func() { interactiveSelect = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"remove", "--interactive"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "plugins.alpha") {
		t.Fatalf("alpha still configured: %s", contents)
	}
	if !strings.Contains(string(contents), "plugins.beta") {
		t.Fatalf("beta lost: %s", contents)
	}
}

// initTestEnv points shelf at a temporary config directory and returns the path.
func initTestEnv(t *testing.T) (directory, configDir, configFile string) {
	t.Helper()
	directory = t.TempDir()
	configDir = filepath.Join(directory, "config")
	configFile = filepath.Join(configDir, "config.toml")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	return directory, configDir, configFile
}

func TestInitPromptsForShellAndConfirm(t *testing.T) {
	_, _, configFile := initTestEnv(t)
	originalShell := initShellPrompt
	originalConfirm := initConfirmPrompt
	initShellPrompt = func(_ *bufio.Reader, out io.Writer) (config.Shell, error) {
		_, _ = fmt.Fprintln(out, "shell prompt")
		return config.Zsh, nil
	}
	initConfirmPrompt = func(path string, _ *bufio.Reader, out io.Writer) (bool, error) {
		if path != configFile {
			t.Errorf("confirm path = %q, want %q", path, configFile)
		}
		_, _ = fmt.Fprintln(out, "confirm prompt")
		return true, nil
	}
	t.Cleanup(func() {
		initShellPrompt = originalShell
		initConfirmPrompt = originalConfirm
	})

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"init"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	want := "shell prompt\nconfirm prompt\n✓ initialized zsh config at " + configFile + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != config.DefaultConfig(config.Zsh) {
		t.Fatalf("config contents = %q, want %q", contents, config.DefaultConfig(config.Zsh))
	}
}

func TestInitDeclinedByUser(t *testing.T) {
	_, _, configFile := initTestEnv(t)
	originalShell := initShellPrompt
	originalConfirm := initConfirmPrompt
	initShellPrompt = func(_ *bufio.Reader, _ io.Writer) (config.Shell, error) {
		return config.Bash, nil
	}
	initConfirmPrompt = func(_ string, _ *bufio.Reader, _ io.Writer) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() {
		initShellPrompt = originalShell
		initConfirmPrompt = originalConfirm
	})

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"init"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when declined", stdout.String())
	}
	if !strings.Contains(stderr.String(), "aborted") {
		t.Fatalf("stderr = %q, missing aborted message", stderr.String())
	}
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Fatalf("config created despite declined confirmation: %v", err)
	}
}

func TestInitShellFlagSkipsShellPrompt(t *testing.T) {
	_, _, configFile := initTestEnv(t)
	originalShell := initShellPrompt
	originalConfirm := initConfirmPrompt
	initShellPrompt = func(_ *bufio.Reader, _ io.Writer) (config.Shell, error) {
		t.Fatal("shell prompt shown despite --shell flag")
		return "", nil
	}
	initConfirmPrompt = func(_ string, _ *bufio.Reader, _ io.Writer) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() {
		initShellPrompt = originalShell
		initConfirmPrompt = originalConfirm
	})

	var stdout bytes.Buffer
	if err := Execute([]string{"init", "--shell", "bash"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "initialized bash config at "+configFile+"\n") {
		t.Fatalf("stdout = %q, missing bash success message", stdout.String())
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != config.DefaultConfig(config.Bash) {
		t.Fatalf("config contents = %q, want %q", contents, config.DefaultConfig(config.Bash))
	}
}

func TestInitShellFlagBeatsInvalidShellEnvironment(t *testing.T) {
	for _, test := range []struct {
		name      string
		envShell  string
		flagShell string
		wantErr   string
		want      string
	}{
		// The --shell flag is explicit and outranks an invalid SHELF_SHELL.
		{name: "flag beats invalid env", envShell: "fish", flagShell: "bash", want: "bash"},
		{name: "flag beats valid env", envShell: "zsh", flagShell: "bash", want: "bash"},
		// Without the flag the invalid environment still fails.
		{name: "invalid env without flag", envShell: "fish", wantErr: "SHELF_SHELL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, configFile := initTestEnv(t)
			t.Setenv("SHELF_SHELL", test.envShell)
			var stdout bytes.Buffer
			args := []string{"init", "--non-interactive"}
			if test.flagShell != "" {
				args = append(args, "--shell", test.flagShell)
			}
			err := Execute(args, &stdout, &bytes.Buffer{})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("err = %v, want error containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(configFile)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != config.DefaultConfig(config.Shell(test.want)) {
				t.Fatalf("config contents = %q, want %q", contents, config.DefaultConfig(config.Shell(test.want)))
			}
		})
	}
}

func TestInitNonInteractiveSkipsPrompts(t *testing.T) {
	_, _, configFile := initTestEnv(t)
	t.Setenv("SHELF_SHELL", "bash")
	originalShell := initShellPrompt
	originalConfirm := initConfirmPrompt
	initShellPrompt = func(_ *bufio.Reader, _ io.Writer) (config.Shell, error) {
		t.Fatal("shell prompt shown in non-interactive mode")
		return "", nil
	}
	initConfirmPrompt = func(_ string, _ *bufio.Reader, _ io.Writer) (bool, error) {
		t.Fatal("confirm prompt shown in non-interactive mode")
		return false, nil
	}
	t.Cleanup(func() {
		initShellPrompt = originalShell
		initConfirmPrompt = originalConfirm
	})

	var stdout bytes.Buffer
	if err := Execute([]string{"init", "--non-interactive"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "initialized bash config at "+configFile+"\n") {
		t.Fatalf("stdout = %q, missing bash success message", stdout.String())
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != config.DefaultConfig(config.Bash) {
		t.Fatalf("config contents = %q, want %q", contents, config.DefaultConfig(config.Bash))
	}
}

func TestInitDoesNotOverwriteExistingConfig(t *testing.T) {
	_, configDir, configFile := initTestEnv(t)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("# user config\n")
	if err := os.WriteFile(configFile, original, 0o600); err != nil {
		t.Fatal(err)
	}
	originalShell := initShellPrompt
	originalConfirm := initConfirmPrompt
	initShellPrompt = func(_ *bufio.Reader, _ io.Writer) (config.Shell, error) {
		t.Fatal("shell prompt shown when config already exists")
		return "", nil
	}
	initConfirmPrompt = func(_ string, _ *bufio.Reader, _ io.Writer) (bool, error) {
		t.Fatal("confirm prompt shown when config already exists")
		return false, nil
	}
	t.Cleanup(func() {
		initShellPrompt = originalShell
		initConfirmPrompt = originalConfirm
	})

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"init"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when config already exists", stdout.String())
	}
	if !strings.Contains(stderr.String(), "config already exists at "+configFile) {
		t.Fatalf("stderr = %q, missing already-exists message", stderr.String())
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(original) {
		t.Fatalf("existing config overwritten: %q", contents)
	}
}

func TestInitExistingConfigInNonInteractiveMode(t *testing.T) {
	_, configDir, configFile := initTestEnv(t)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("# user config\n")
	if err := os.WriteFile(configFile, original, 0o600); err != nil {
		t.Fatal(err)
	}
	originalShell := initShellPrompt
	originalConfirm := initConfirmPrompt
	initShellPrompt = func(_ *bufio.Reader, _ io.Writer) (config.Shell, error) {
		t.Fatal("shell prompt shown in non-interactive mode")
		return "", nil
	}
	initConfirmPrompt = func(_ string, _ *bufio.Reader, _ io.Writer) (bool, error) {
		t.Fatal("confirm prompt shown in non-interactive mode")
		return false, nil
	}
	t.Cleanup(func() {
		initShellPrompt = originalShell
		initConfirmPrompt = originalConfirm
	})

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"init", "--non-interactive"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when config already exists", stdout.String())
	}
	if !strings.Contains(stderr.String(), "config already exists at "+configFile) {
		t.Fatalf("stderr = %q, missing already-exists message", stderr.String())
	}
}

func TestInitPromptErrorStopsInit(t *testing.T) {
	_, _, configFile := initTestEnv(t)
	originalShell := initShellPrompt
	originalConfirm := initConfirmPrompt
	initShellPrompt = func(_ *bufio.Reader, _ io.Writer) (config.Shell, error) {
		return "", errors.New("stdin closed")
	}
	initConfirmPrompt = func(_ string, _ *bufio.Reader, _ io.Writer) (bool, error) {
		t.Fatal("confirm prompt shown after shell prompt failed")
		return false, nil
	}
	t.Cleanup(func() {
		initShellPrompt = originalShell
		initConfirmPrompt = originalConfirm
	})

	if err := Execute([]string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "stdin closed") {
		t.Fatalf("err = %v, want stdin-closed error", err)
	}
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Fatalf("config created despite prompt error: %v", err)
	}
}

func TestListPrintsLockedPluginNames(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.first]\ninline = \"echo first\"\n\n[plugins.second]\ninline = \"echo second\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Execute([]string{"list"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "first\nsecond\n" {
		t.Fatalf("list output = %q", output.String())
	}
}

func TestInfoReportsLockedGitPlugin(t *testing.T) {
	directory := t.TempDir()
	repository := filepath.Join(directory, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	revision := gitCommit(t, repository, "plugin.zsh", "echo info\n")

	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.test]\ngit = \"" + repository + "\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	checkout, err := source.GitDirectory(filepath.Join(directory, "data"), source.Request{Git: repository})
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Execute([]string{"info", "test"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	wantPrefix := "- source: \"git:" + repository + "\"\n" +
		"- rev: \"" + revision + "\"\n" +
		"- files: \"" + filepath.Join(checkout, "plugin.zsh") + "\"\n"
	if !strings.HasPrefix(output.String(), wantPrefix) {
		t.Fatalf("info output = %q, want prefix %q", output.String(), wantPrefix)
	}
	if !strings.Contains(output.String(), "- size: \"") {
		t.Fatalf("info output = %q, want an installed size line", output.String())
	}
}

func TestInfoReportsLocalPluginSourceAndSize(t *testing.T) {
	directory := t.TempDir()
	pluginDir := filepath.Join(directory, "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pluginFile := filepath.Join(pluginDir, "demo.zsh")
	if err := os.WriteFile(pluginFile, []byte("echo local\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\nlocal = \"" + pluginDir + "\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Execute([]string{"info", "demo"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	wantPrefix := "- source: \"" + pluginDir + "\"\n" +
		"- files: \"" + pluginFile + "\"\n"
	if !strings.HasPrefix(output.String(), wantPrefix) {
		t.Fatalf("info output = %q, want prefix %q", output.String(), wantPrefix)
	}
	if !strings.Contains(output.String(), "- size: \"") {
		t.Fatalf("info output = %q, want an installed size line", output.String())
	}
}

func TestInfoShowsInlinePluginWithoutSize(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Execute([]string{"info", "test"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "- source: \"inline\"\n" {
		t.Fatalf("info output = %q, want only the inline source", output.String())
	}
}

func TestInfoRejectsUnknownPlugin(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var output, stderr bytes.Buffer
	if err := Execute([]string{"info", "missing"}, &output, &stderr); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v, want an error naming the missing plugin", err)
	}
	if output.Len() != 0 {
		t.Fatalf("info stdout = %q, want empty for an unknown plugin", output.String())
	}
	if !strings.Contains(stderr.String(), "missing") {
		t.Fatalf("info stderr = %q, want the missing plugin name", stderr.String())
	}
}

func TestInfoColorModes(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	always := func() string {
		var output bytes.Buffer
		if err := Execute([]string{"info", "test", "--color", "always"}, &output, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}()
	for _, expected := range []string{"\x1b[1;35msource\x1b[0m", "\x1b[1;32m\"inline\"\x1b[0m"} {
		if !strings.Contains(always, expected) {
			t.Errorf("colored info output missing %q: %q", expected, always)
		}
	}

	var never bytes.Buffer
	if err := Execute([]string{"info", "test", "--color", "never"}, &never, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(never.String(), '\x1b') {
		t.Errorf("--color never emitted escape sequences: %q", never.String())
	}
	if never.String() != "- source: \"inline\"\n" {
		t.Fatalf("--color never info output = %q, want plain output", never.String())
	}

	var auto bytes.Buffer
	if err := Execute([]string{"info", "test"}, &auto, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(auto.String(), '\x1b') {
		t.Errorf("auto color emitted escapes to a non-terminal: %q", auto.String())
	}
}

func TestStatusReportsHealthyLockedPlugins(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.first]\ninline = \"echo first\"\n\n[plugins.second]\ninline = \"echo second\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Execute([]string{"status"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "first: ok\nsecond: ok\n" {
		t.Fatalf("status output = %q", output.String())
	}
}

func TestStatusReportsDriftedGitRevision(t *testing.T) {
	directory := t.TempDir()
	repository := filepath.Join(directory, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	lockedRevision := gitCommit(t, repository, "plugin.zsh", "echo first\n")
	currentRevision := gitCommit(t, repository, "plugin.zsh", "echo second\n")

	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.test]\ngit = \"" + repository + "\"\nrev = \"" + lockedRevision + "\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	checkout, err := source.GitDirectory(filepath.Join(directory, "data"), source.Request{Git: repository})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", checkout, "checkout", "--detach", currentRevision).CombinedOutput(); err != nil {
		t.Fatalf("checkout drifted revision: %v\n%s", err, output)
	}

	var output bytes.Buffer
	if err := Execute([]string{"status"}, &output, &bytes.Buffer{}); err == nil {
		t.Fatal("status succeeded for a drifted revision")
	}
	want := "test: revision " + currentRevision + ", want " + lockedRevision + "\n"
	if output.String() != want {
		t.Fatalf("status output = %q, want %q", output.String(), want)
	}
}

func TestStatusChecksGitRevisionsInParallelAndKeepsOrder(t *testing.T) {
	directory := t.TempDir()
	firstRepository := filepath.Join(directory, "first")
	secondRepository := filepath.Join(directory, "second")
	if err := os.MkdirAll(firstRepository, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secondRepository, 0o755); err != nil {
		t.Fatal(err)
	}
	firstRevision := gitCommit(t, firstRepository, "plugin.zsh", "echo first\n")
	lockedSecond := gitCommit(t, secondRepository, "plugin.zsh", "echo second\n")
	currentSecond := gitCommit(t, secondRepository, "plugin.zsh", "echo drifted\n")

	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n" +
		"[plugins.first]\ngit = \"" + firstRepository + "\"\nrev = \"" + firstRevision + "\"\n\n" +
		"[plugins.second]\ngit = \"" + secondRepository + "\"\nrev = \"" + lockedSecond + "\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// Drift the second checkout so the two git checks disagree; both still report in declaration order despite running concurrently.
	checkout, err := source.GitDirectory(filepath.Join(directory, "data"), source.Request{Git: secondRepository})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", checkout, "checkout", "--detach", currentSecond).CombinedOutput(); err != nil {
		t.Fatalf("checkout drifted revision: %v\n%s", err, output)
	}

	var output bytes.Buffer
	if err := Execute([]string{"status"}, &output, &bytes.Buffer{}); err == nil {
		t.Fatal("status succeeded for a drifted revision")
	}
	want := "first: ok\nsecond: revision " + currentSecond + ", want " + lockedSecond + "\n"
	if output.String() != want {
		t.Fatalf("status output = %q, want %q", output.String(), want)
	}
}

func TestStatusCancelsAWedgedGit(t *testing.T) {
	// A stalled git must be killed by status's own deadline; otherwise status hangs forever.
	originalTimeout := gitStatusTimeout
	gitStatusTimeout = 50 * time.Millisecond
	t.Cleanup(func() { gitStatusTimeout = originalTimeout })

	directory := t.TempDir()
	repository := filepath.Join(directory, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	revision := gitCommit(t, repository, "plugin.zsh", "echo revision\n")

	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.test]\ngit = \"" + repository + "\"\nrev = \"" + revision + "\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	// Wedge gitHead: hand the context back, then block until it is cancelled.
	originalGitHead := gitHead
	contexts := make(chan context.Context, 1)
	gitHead = func(ctx context.Context, _ string) ([]byte, error) {
		contexts <- ctx
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { gitHead = originalGitHead })

	// status must return promptly: only real cancellation unblocks a wedged git.
	type statusResult struct {
		err    error
		output string
	}
	done := make(chan statusResult, 1)
	go func() {
		var output bytes.Buffer
		err := Execute([]string{"status"}, &output, &bytes.Buffer{})
		done <- statusResult{err: err, output: output.String()}
	}()
	var result statusResult
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("status hung on a wedged git; cancellation is not real")
	}
	ctx := <-contexts
	if ctx == nil {
		t.Fatal("gitHead received a nil context")
	}
	// Cancellation must be real: a deadline ctx so a wedged git actually gets killed.
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("gitHead context has no deadline; a wedged git could never be cancelled")
	}
	if result.err == nil {
		t.Fatal("status succeeded despite a wedged git that had to be cancelled")
	}
	want := "test: unable to read revision\n"
	if result.output != want {
		t.Fatalf("status output = %q, want %q", result.output, want)
	}
}

func TestDoctorReportsHealthyConfigurationAndLock(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Execute([]string{"doctor"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	shellPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	versionOutput, err := exec.Command(shellPath, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	versionLine := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(versionOutput)), "\n", 2)[0])
	want := fmt.Sprintf("version:  shelf %s\nshell:    %s\n          %s\n\nconfig:   ok  %s\nlock:     ok  %s\n\nNo problems found\n", Version, shellPath, versionLine, configFile, filepath.Join(directory, "data", "plugins.lock"))
	if output.String() != want {
		t.Fatalf("doctor output = %q, want %q", output.String(), want)
	}
}

func TestCleanRemovesUnconfiguredPluginDirectories(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.current]\ninline = \"echo current\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Inline plugins are stored in the lock file, so their old install directories are unowned.
	for _, name := range []string{"current", "obsolete"} {
		if err := os.MkdirAll(filepath.Join(dataDir, "plugins", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	var output bytes.Buffer
	if err := Execute([]string{"clean"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "removed: plugins/current\nremoved: plugins/obsolete\n✓ cleaned: 2 paths\n" {
		t.Fatalf("clean output = %q", output.String())
	}
	for _, name := range []string{"current", "obsolete"} {
		if _, err := os.Stat(filepath.Join(dataDir, "plugins", name)); !os.IsNotExist(err) {
			t.Fatalf("plugin directory %q remains: %v", name, err)
		}
	}
}

func TestCleanKeepsOwnedSourcesAndPrunesTheRest(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.kept]\ngithub = \"rubiin/kept\"\n\n[plugins.inline]\ninline = \"echo inline\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	directories := []string{
		filepath.Join(dataDir, "repos", "github.com", "rubiin", "kept"),
		filepath.Join(dataDir, "repos", "github.com", "rubiin", "gone"),
		filepath.Join(dataDir, "repos", "example.com"),
		filepath.Join(dataDir, "downloads", "example.com"),
		filepath.Join(dataDir, "plugins", "inline"),
		filepath.Join(dataDir, "plugins", "obsolete"),
	}
	for _, path := range directories {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	var output bytes.Buffer
	if err := Execute([]string{"clean"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	want := "removed: repos/example.com\n" +
		"removed: repos/github.com/rubiin/gone\n" +
		"removed: downloads/example.com\n" +
		"removed: plugins/inline\n" +
		"removed: plugins/obsolete\n" +
		"✓ cleaned: 5 paths\n"
	if output.String() != want {
		t.Fatalf("clean output = %q, want %q", output.String(), want)
	}
	if _, err := os.Stat(directories[0]); err != nil {
		t.Fatalf("owned source was removed: %v", err)
	}
	for _, path := range []string{directories[4], directories[5]} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unowned plugin directory %q remains: %v", path, err)
		}
	}
}

func TestCleanReportsNothingToRemove(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\nlocal = \"plugins/demo\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	var output bytes.Buffer
	if err := Execute([]string{"clean"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "✓ nothing to clean\n" {
		t.Fatalf("clean output = %q", output.String())
	}
}

func TestRemoveInteractiveRemovesSelected(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.alpha]\ninline = \"echo a\"\n\n[plugins.beta]\ninline = \"echo b\"\n\n[plugins.gamma]\ninline = \"echo g\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	original := interactiveSelect
	interactiveSelect = func(options []string, _ io.Writer) ([]string, error) {
		if len(options) != 3 {
			t.Errorf("picker options = %v, want alpha, beta, gamma", options)
		}
		return []string{"beta"}, nil
	}
	t.Cleanup(func() { interactiveSelect = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"remove", "--interactive"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "removed: beta") {
		t.Fatalf("stdout = %q, want removed: beta", stdout.String())
	}
	if !strings.Contains(stderr.String(), "shelf lock") {
		t.Fatalf("stderr = %q, want relock hint", stderr.String())
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "plugins.beta") {
		t.Fatalf("beta still configured: %s", contents)
	}
	if !strings.Contains(string(contents), "plugins.alpha") || !strings.Contains(string(contents), "plugins.gamma") {
		t.Fatalf("unselected plugins lost: %s", contents)
	}
}

func TestRemoveInteractiveCancelledKeepsConfig(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	original := "shell = \"zsh\"\n\n[plugins.alpha]\ninline = \"echo a\"\n"
	if err := os.WriteFile(configFile, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	originalSelect := interactiveSelect
	interactiveSelect = func(_ []string, _ io.Writer) ([]string, error) {
		return nil, tui.ErrCancelled
	}
	t.Cleanup(func() { interactiveSelect = originalSelect })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"remove", "--interactive"}, &stdout, &stderr); err != nil {
		t.Fatalf("cancel should not be an error: %v", err)
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("config changed after cancel: %s", contents)
	}
	if !strings.Contains(stderr.String(), "cancelled") {
		t.Fatalf("stderr = %q, want cancelled message", stderr.String())
	}
}

func TestUpdateInteractiveUpdatesOnlySelected(t *testing.T) {
	directory := t.TempDir()
	repository := filepath.Join(directory, "repo")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommit(t, repository, "plugin.zsh", "echo one\n")
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\ngit = \"" + repository + "\"\n\n[plugins.other]\ninline = \"echo other\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	second := gitCommit(t, repository, "plugin.zsh", "echo two\n")

	original := interactiveSelect
	interactiveSelect = func(options []string, _ io.Writer) ([]string, error) {
		if len(options) != 2 || options[0] != "demo" || options[1] != "other" {
			t.Errorf("picker options = %v, want demo, other", options)
		}
		return []string{"demo"}, nil
	}
	t.Cleanup(func() { interactiveSelect = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"update", "-i"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	locked, err := lock.Read(filepath.Join(directory, "data", "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 2 {
		t.Fatalf("locked plugins = %d, want 2 (unselected plugins stay locked)", len(locked.Plugins))
	}
	for _, plugin := range locked.Plugins {
		if plugin.Name == "demo" {
			if plugin.Rev != second {
				t.Fatalf("plugin %q revision = %q, want updated %q", plugin.Name, plugin.Rev, second)
			}
			continue
		}
		if plugin.Name != "other" {
			t.Fatalf("unexpected locked plugin %q", plugin.Name)
		}
		// Inline plugins carry no revision; the unselected inline text must survive untouched.
		if plugin.Inline != "echo other" {
			t.Fatalf("plugin %q inline = %q, want %q", plugin.Name, plugin.Inline, "echo other")
		}
	}
}

func TestUpdateInteractiveCancelledStaysPinned(t *testing.T) {
	directory := t.TempDir()
	repository := filepath.Join(directory, "repo")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	rev := gitCommit(t, repository, "plugin.zsh", "echo one\n")
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\ngit = \"" + repository + "\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	gitCommit(t, repository, "plugin.zsh", "echo two\n")

	originalSelect := interactiveSelect
	interactiveSelect = func(_ []string, _ io.Writer) ([]string, error) {
		return nil, tui.ErrCancelled
	}
	t.Cleanup(func() { interactiveSelect = originalSelect })

	var stderr bytes.Buffer
	if err := Execute([]string{"update", "--interactive"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("cancel should not be an error: %v", err)
	}
	if !strings.Contains(stderr.String(), "cancelled") {
		t.Fatalf("stderr = %q, want cancelled message", stderr.String())
	}
	locked, err := lock.Read(filepath.Join(directory, "data", "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 || locked.Plugins[0].Rev != rev {
		t.Fatalf("locked plugins = %+v, want the original revision %q preserved", locked.Plugins, rev)
	}
}

func TestUpdateInteractiveRejectsLockFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Execute([]string{"update", "--interactive", "--lock"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when --lock is combined with --interactive")
	}
	if !strings.Contains(err.Error(), "--lock cannot be combined with --interactive") {
		t.Fatalf("error = %v, want the --lock conflict error", err)
	}
}

func TestCleanInteractiveRemovesOnlySelected(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.current]\ninline = \"echo current\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"obsolete", "stale"} {
		if err := os.MkdirAll(filepath.Join(dataDir, "plugins", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	original := interactiveSelect
	interactiveSelect = func(options []string, _ io.Writer) ([]string, error) {
		if len(options) != 2 || options[0] != "plugins/obsolete" || options[1] != "plugins/stale" {
			t.Errorf("picker options = %v, want plugins/obsolete, plugins/stale", options)
		}
		return []string{"plugins/stale"}, nil
	}
	t.Cleanup(func() { interactiveSelect = original })

	var stdout bytes.Buffer
	if err := Execute([]string{"clean", "-i"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "removed: plugins/stale\n✓ cleaned: 1 paths\n" {
		t.Fatalf("clean output = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "plugins", "stale")); !os.IsNotExist(err) {
		t.Fatalf("selected directory remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "plugins", "obsolete")); err != nil {
		t.Fatalf("unselected directory was removed: %v", err)
	}
}

func TestCleanInteractiveCancelledRemovesNothing(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.current]\ninline = \"echo current\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dataDir, "plugins", "obsolete")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	originalSelect := interactiveSelect
	interactiveSelect = func(_ []string, _ io.Writer) ([]string, error) {
		return nil, tui.ErrCancelled
	}
	t.Cleanup(func() { interactiveSelect = originalSelect })

	var stderr bytes.Buffer
	if err := Execute([]string{"clean", "--interactive"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("cancel should not be an error: %v", err)
	}
	if !strings.Contains(stderr.String(), "cancelled") {
		t.Fatalf("stderr = %q, want cancelled message", stderr.String())
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("canceled clean removed %q: %v", stale, err)
	}
}

func TestCleanInteractiveNothingToClean(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.demo]\nlocal = \"plugins/demo\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	original := interactiveSelect
	interactiveSelect = func(_ []string, _ io.Writer) ([]string, error) {
		t.Fatal("picker shown with nothing to clean")
		return nil, nil
	}
	t.Cleanup(func() { interactiveSelect = original })

	var output bytes.Buffer
	if err := Execute([]string{"clean", "--interactive"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "✓ nothing to clean\n" {
		t.Fatalf("clean output = %q", output.String())
	}
}

func TestRemoveInteractiveRejectsNameArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"remove", "--interactive", "alpha"}, &stdout, &stderr); err == nil {
		t.Fatal("expected error when NAME is combined with --interactive")
	}
}

func TestRemoveInteractiveRequiresTerminal(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.alpha]\ninline = \"echo a\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"remove", "--interactive"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("err = %v, want terminal error", err)
	}
}

func TestSourceRestoresLockedGitRevision(t *testing.T) {
	directory := t.TempDir()
	repository := filepath.Join(directory, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	commit := gitCommit(t, repository, "plugin.zsh", "echo first\n")
	_ = gitCommit(t, repository, "plugin.zsh", "echo second\n")

	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.test]\ngit = \"" + repository + "\"\nrev = \"" + commit + "\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	checkout, err := source.GitDirectory(filepath.Join(directory, "data"), source.Request{Git: repository})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", checkout, "checkout", "--detach", "HEAD").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", checkout, "checkout", "--detach", "master").Run(); err != nil {
		t.Fatal(err)
	}

	if err := Execute([]string{"source"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(checkout, "plugin.zsh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "echo first\n" {
		t.Fatalf("checked out plugin = %q", contents)
	}
}

func gitCommit(t *testing.T, directory, file, contents string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(directory, ".git")); os.IsNotExist(err) {
		for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Shelf Tests"}} {
			if output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, output)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(directory, file), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", file}, {"commit", "-m", contents}} {
		if output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	output, err := exec.Command("git", "-C", directory, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func TestLockReportsProgressOnStderr(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"lock"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	func() {
		var stderr bytes.Buffer
		if err := Execute([]string{"lock", "--color", "always"}, &bytes.Buffer{}, &stderr); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"\x1b[1;35mLoaded\x1b[0m", "\x1b[1;36m   Checked\x1b[0m", "\x1b[1;35mLocked\x1b[0m"} {
			if !strings.Contains(stderr.String(), expected) {
				t.Errorf("colored lock diagnostics missing %q: %q", expected, stderr.String())
			}
		}
	}()
	func() {
		var stderr bytes.Buffer
		if err := Execute([]string{"lock", "--color", "never"}, &bytes.Buffer{}, &stderr); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(stderr.String(), '\x1b') {
			t.Errorf("--color never emitted escape sequences: %q", stderr.String())
		}
		for _, expected := range []string{"Loaded ", "Locked "} {
			if !strings.Contains(stderr.String(), expected) {
				t.Errorf("--color never diagnostics missing %q: %q", expected, stderr.String())
			}
		}
	}()
	func() {
		var stderr bytes.Buffer
		if err := Execute([]string{"lock", "--update"}, &bytes.Buffer{}, &stderr); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(stderr.String(), '\x1b') {
			t.Errorf("auto color emitted escapes to a non-terminal: %q", stderr.String())
		}
	}()
	if stdout.Len() != 0 {
		t.Fatalf("lock stdout = %q", stdout.String())
	}
}

// checkedSources returns the plugin sources the lock diagnostics printed, in order.
func checkedSources(stderr string) []string {
	var sources []string
	for _, line := range strings.Split(stderr, "\n") {
		if index := strings.Index(line, "Checked "); index >= 0 {
			sources = append(sources, line[index+len("Checked "):])
		}
	}
	return sources
}

func TestLockReportsSkippedPlugins(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.always]\ninline = \"echo always\"\n\n[plugins.work]\nprofiles = [\"work\"]\ngithub = \"rubiin/work\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var stderr bytes.Buffer
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := checkedSources(stderr.String()); !slices.Equal(got, []string{"inline"}) {
		t.Fatalf("checked sources = %q, want [inline]", got)
	}
	if !strings.Contains(stderr.String(), "   Skipped https://github.com/rubiin/work") {
		t.Fatalf("stderr = %q, want a skipped status for the profiled plugin", stderr.String())
	}
}

func TestSourceReportsUnlockedAndRenderedWhenVerbose(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if err := Execute([]string{"--verbose", "source"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Unlocked ", "   Inlined test"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Errorf("verbose source diagnostics missing %q: %q", expected, stderr.String())
		}
	}
}

func TestFailuresPrintAnErrorLineOnce(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SHELF_CONFIG_FILE", filepath.Join(directory, "absent", "config.toml"))
	t.Setenv("SHELF_CONFIG_DIR", filepath.Join(directory, "absent"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var stderr bytes.Buffer
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &stderr); err == nil {
		t.Fatal("expected a failure for a missing config file")
	}
	if !strings.HasPrefix(stderr.String(), "\nerror: ") {
		t.Fatalf("stderr = %q, want an error line with a status prefix", stderr.String())
	}
	if strings.Count(stderr.String(), "error: ") != 1 {
		t.Fatalf("stderr = %q, want the error reported once", stderr.String())
	}
}

func TestLockDiagnosticsFollowConfigOrder(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Local sources render their own path, so declaration order is observable; it is not alphabetical.
	names := []string{"zeta", "alpha", "mu", "beta", "eta"}
	var declared strings.Builder
	declared.WriteString("shell = \"zsh\"\n")
	want := make([]string, 0, len(names))
	for _, name := range names {
		source := filepath.Join(directory, "sources", name)
		if err := os.MkdirAll(source, 0o755); err != nil {
			t.Fatal(err)
		}
		declared.WriteString("\n[plugins." + name + "]\nlocal = " + strconv.Quote(source) + "\n")
		want = append(want, source)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte(declared.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	// Run repeatedly: map iteration order varies per pass, so nondeterminism fails a pass.
	for pass := range 4 {
		var stderr bytes.Buffer
		if err := Execute([]string{"lock"}, &bytes.Buffer{}, &stderr); err != nil {
			t.Fatal(err)
		}
		if got := checkedSources(stderr.String()); !slices.Equal(got, want) {
			t.Fatalf("pass %d checked sources = %q, want %q", pass, got, want)
		}
	}
}

func TestLockDiagnosticsIncludeDottedKeyPlugins(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Dotted keys never reach PluginOrder, so the fallback must still list them.
	configFile := filepath.Join(configDir, "config.toml")
	contents := "shell = \"zsh\"\n\nplugins.orphan.inline = \"echo orphan\"\n"
	if err := os.WriteFile(configFile, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var stderr bytes.Buffer
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := checkedSources(stderr.String()); !slices.Equal(got, []string{"inline"}) {
		t.Fatalf("checked sources = %q, want [inline]", got)
	}
}

func TestSourceRendersFromTheLockWithoutParsingTheConfig(t *testing.T) {
	// The lock covers these exact bytes, so the hot path renders without decoding them.
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	contents := []byte("shell = zsh\n\n[plugins.demo\ninline = \"echo demo\"\n")
	if err := os.WriteFile(configFile, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(directory, "data")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	fingerprint, err := fingerprintWithRevision(fingerprintWithShell(contents), filepath.Join(configDir, "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	locked := lock.LockedConfig{
		ConfigFingerprint: fingerprint,
		Shell:             "zsh",
		Templates:         map[string]string{"source": "source \"{{ files.0 }}\"\n"},
		Plugins:           []lock.LockedPlugin{{Name: "demo", Inline: "echo demo"}},
	}
	if err := lock.Write(filepath.Join(dataDir, "plugins.lock"), locked); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Execute([]string{"source"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "eval 'source /dev/stdin <<'\\''SHELF_0'\\''\necho demo\nSHELF_0\n'\n" {
		t.Fatalf("source output = %q", output.String())
	}
}

func TestSourceRelocksWhenTheShellOverrideChanges(t *testing.T) {
	// SHELF_SHELL only matters when the config omits `shell`, and the lock remembers it, so dropping the override has to relock.
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("[plugins.demo]\ninline = \"echo demo\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(directory, "data")
	lockPath := filepath.Join(dataDir, "plugins.lock")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)
	t.Setenv("SHELF_SHELL", "bash")

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	locked, err := lock.Read(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if locked.Shell != "bash" {
		t.Fatalf("locked shell = %q, want bash", locked.Shell)
	}

	t.Setenv("SHELF_SHELL", "")
	if err := Execute([]string{"source"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	relocked, err := lock.Read(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if relocked.Shell != "zsh" {
		t.Fatalf("shell after dropping the override = %q, want zsh", relocked.Shell)
	}
}

func TestAddWritesBuildCommands(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"add", "demo", "--local", filepath.Join(directory, "plugin"), "--build", "make", "--build", "cargo build"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `build = ["make", "cargo build"]`) {
		t.Fatalf("config = %s", contents)
	}
}

func TestAddWritesFrozenField(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"add", "demo", "--local", filepath.Join(directory, "plugin"), "--frozen"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "frozen = true") {
		t.Fatalf("config = %s, want the frozen key", contents)
	}
}

func TestBuildHookGeneratesTheSourcedFile(t *testing.T) {
	directory := t.TempDir()
	pluginDirectory := filepath.Join(directory, "plugin")
	if err := os.MkdirAll(pluginDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\nlocal = \"" + pluginDirectory + "\"\nbuild = [\"touch generated.zsh\"]\nuse = [\"generated.zsh\"]\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Execute([]string{"source"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "generated.zsh") {
		t.Fatalf("source output = %q, want the generated file", output.String())
	}
}

func TestQuietSuppressesBuildOutput(t *testing.T) {
	directory := t.TempDir()
	pluginDirectory := filepath.Join(directory, "plugin")
	if err := os.MkdirAll(pluginDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\nlocal = \"" + pluginDirectory + "\"\nbuild = [\"echo built-here\"]\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_QUIET", "false")

	var stderr bytes.Buffer
	if err := Execute([]string{"lock", "--quiet"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("lock: %v (stderr %q)", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "built-here") {
		t.Fatalf("quiet lock streamed build output: %q", stderr.String())
	}
}

func TestLockStreamsBuildOutputToStderr(t *testing.T) {
	directory := t.TempDir()
	pluginDirectory := filepath.Join(directory, "plugin")
	if err := os.MkdirAll(pluginDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\nlocal = \"" + pluginDirectory + "\"\nbuild = [\"echo built-here\"]\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_QUIET", "false")

	var stderr bytes.Buffer
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("lock: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "built-here") {
		t.Fatalf("lock stderr = %q, want build output", stderr.String())
	}
}

// TestRemoteUpdateUsesConditionalGet verifies the whole pipeline: lock records the response
// validator, and the next update sends it as If-None-Match, so a 304 skips re-download.
func TestRemoteUpdateUsesConditionalGet(t *testing.T) {
	var unconditional, conditional, bodyWrites int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("If-None-Match") == `"573a1e10"` {
			conditional++
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		unconditional++
		writer.Header().Set("ETag", `"573a1e10"`)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("echo remote-e2e\n"))
		bodyWrites++
	}))
	defer server.Close()

	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\nremote = \"" + server.URL + "/demo.plugin.zsh\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(directory, "data", "plugins.lock")
	locked, err := lock.Read(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 || locked.Plugins[0].ETag != `"573a1e10"` {
		t.Fatalf("locked plugins = %+v, want one recording the validator", locked.Plugins)
	}

	var output bytes.Buffer
	if err := Execute([]string{"update"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "demo.plugin.zsh") {
		t.Fatalf("update output = %q", output.String())
	}
	// The 304 must leave the installed file untouched.
	installedFile := filepath.Join(directory, "data", "downloads", "127.0.0.1", "demo.plugin.zsh")
	contents, err := os.ReadFile(installedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "echo remote-e2e\n" {
		t.Fatalf("installed file = %q", contents)
	}
	if unconditional != 1 {
		t.Fatalf("unconditional downloads = %d, want 1 for the first lock", unconditional)
	}
	if conditional != 1 {
		t.Fatalf("conditional downloads = %d, want 1 for the update", conditional)
	}
	if bodyWrites != 1 {
		t.Fatalf("body writes = %d, want 1; the 304 must skip re-download", bodyWrites)
	}
}

func TestLockRecordsFrozenPlugin(t *testing.T) {
	directory := t.TempDir()
	repository := filepath.Join(directory, "repo")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommit(t, repository, "plugin.zsh", "echo frozen\n")
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\ngit = \"" + repository + "\"\nfrozen = true\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	locked, err := lock.Read(filepath.Join(directory, "data", "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 || !locked.Plugins[0].Frozen {
		t.Fatalf("locked plugins = %+v, want the frozen flag recorded", locked.Plugins)
	}
}

func TestUpdateMarksFrozenPlugins(t *testing.T) {
	directory := t.TempDir()
	repository := filepath.Join(directory, "repo")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	rev := gitCommit(t, repository, "plugin.zsh", "echo frozen\n")
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.demo]\ngit = \"" + repository + "\"\nfrozen = true\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if err := Execute([]string{"update"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("update: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Frozen "+repository) {
		t.Fatalf("update stderr = %q, want a Frozen status for the pinned plugin", stderr.String())
	}
	// --force refreshes frozen plugins, so it must not report them as Frozen.
	stderr.Reset()
	if err := Execute([]string{"update", "--force"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("update --force: %v (stderr %q)", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "Frozen ") {
		t.Fatalf("update --force stderr = %q, want no Frozen status", stderr.String())
	}
	// lock --update shares the Frozen marking.
	stderr.Reset()
	if err := Execute([]string{"lock", "--update"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("lock --update: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Frozen "+repository) {
		t.Fatalf("lock --update stderr = %q, want a Frozen status", stderr.String())
	}
	// The frozen plugin stays pinned to its original revision.
	locked, err := lock.Read(filepath.Join(directory, "data", "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 || locked.Plugins[0].Rev != rev {
		t.Fatalf("locked plugins = %+v, want the original revision %q preserved", locked.Plugins, rev)
	}
}

func TestForceRequiresUpdateOrReinstall(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	for _, command := range [][]string{{"lock", "--force"}, {"source", "--force"}} {
		var stderr bytes.Buffer
		err := Execute(command, &bytes.Buffer{}, &stderr)
		if err == nil {
			t.Fatalf("%v succeeded; want a --force validation error", command)
		}
		if !strings.Contains(err.Error(), "--force requires --update or --reinstall") {
			t.Fatalf("%v error = %v, want the --force validation error", command, err)
		}
	}
}

func TestVersionFlagPrintsVersion(t *testing.T) {
	var output bytes.Buffer
	if err := Execute([]string{"--version"}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if want := "shelf version " + Version + "\n"; output.String() != want {
		t.Fatalf("version output = %q, want %q", output.String(), want)
	}
}

// TestVersionIsNotACommand covers the shell update from `shelf version` to `shelf --version`.
func TestVersionIsNotACommand(t *testing.T) {
	err := Execute([]string{"version"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("`shelf version` still runs as a subcommand; it should be a --version flag")
	}
}

func TestUpdateSticksAcrossTheNextFastPathSource(t *testing.T) {
	directory := t.TempDir()
	repository := filepath.Join(directory, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommit(t, repository, "plugin.zsh", "echo first\n")
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ngit = \"file://"+repository+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(directory, "data")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	next := gitCommit(t, repository, "plugin.zsh", "echo second\n")
	if err := Execute([]string{"update"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"source"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	checkout, err := source.GitDirectory(dataDir, source.Request{Git: "file://" + repository})
	if err != nil {
		t.Fatal(err)
	}
	head, err := exec.Command("git", "-C", checkout, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(head)); got != next {
		t.Fatalf("after update + source, checkout HEAD = %q, want the updated revision %q", got, next)
	}
}

// TestSourceRestoresOnlyUnderTheExclusiveLock holds a shared config lock, as a concurrent
// shell startup would: an up-to-date fast path still renders, but a restore, which writes
// to the clone, must wait for exclusive access instead of racing the other process.
func TestSourceRestoresOnlyUnderTheExclusiveLock(t *testing.T) {
	directory := t.TempDir()
	repository := filepath.Join(directory, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommit(t, repository, "plugin.zsh", "echo first\n")
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ngit = \"file://"+repository+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(directory, "data")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	checkout, err := source.GitDirectory(dataDir, source.Request{Git: "file://" + repository})
	if err != nil {
		t.Fatal(err)
	}
	locked := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD"))

	shared, err := filelock.Acquire(configDir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shared.Release() }()
	if err := Execute([]string{"source"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("up-to-date source under a shared lock: %v", err)
	}

	drifted := gitCommit(t, repository, "plugin.zsh", "echo second\n")
	gitOutput(t, checkout, "fetch", "-q", "origin")
	gitOutput(t, checkout, "checkout", "-q", "--detach", drifted)
	finished := make(chan error, 1)
	go func() { finished <- Execute([]string{"source"}, &bytes.Buffer{}, &bytes.Buffer{}) }()
	select {
	case err := <-finished:
		t.Fatalf("source restored while another process held the shared lock (err = %v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	if got := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD")); got != drifted {
		t.Fatalf("checkout moved to %q before the exclusive lock was granted", got)
	}
	if err := shared.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("source did not finish after the shared lock was released")
	}
	if got := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD")); got != locked {
		t.Fatalf("restored HEAD = %q, want the locked revision %q", got, locked)
	}
}

func gitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func TestLockCountsOnlyThePluginsWrittenToTheLock(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.always]\ninline = \"echo always\"\n\n" +
		"[plugins.work]\nprofiles = [\"work\"]\ninline = \"echo work\"\n\n" +
		"[plugins.missing]\nlocal = \"" + filepath.Join(directory, "missing") + "\"\noptional = true\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_COLOR", "never")
	var stderr bytes.Buffer
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("lock: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Locked 1 plugins") {
		t.Fatalf("lock stderr = %q, want the one plugin that reached the lock counted", stderr.String())
	}
}

func TestBoolEnvironmentIsParsedStrictly(t *testing.T) {
	for _, value := range []string{"", "1", "0", "true", "TRUE", "False", " 1 ", "t"} {
		t.Setenv("SHELF_VERBOSE", value)
		if err := validateBoolEnvironment(); err != nil {
			t.Errorf("SHELF_VERBOSE=%q rejected: %v", value, err)
		}
	}
	t.Setenv("SHELF_VERBOSE", "TRUE")
	if !envBool("SHELF_VERBOSE") {
		t.Error("SHELF_VERBOSE=TRUE did not enable verbose")
	}
	t.Setenv("SHELF_VERBOSE", "")
	for _, value := range []string{"yes", "on", "2", "truee"} {
		t.Setenv("SHELF_QUIET", value)
		var stderr bytes.Buffer
		err := Execute([]string{"path"}, &bytes.Buffer{}, &stderr)
		if err == nil || !strings.Contains(err.Error(), "SHELF_QUIET") {
			t.Errorf("SHELF_QUIET=%q err = %v, want a rejection naming the variable", value, err)
		}
	}
}

func TestFingerprintWithRevisionFailsOnAnUnreadableManifest(t *testing.T) {
	directory := t.TempDir()
	missing, err := fingerprintWithRevision("base", filepath.Join(directory, "absent.lock"))
	if err != nil || missing == "" {
		t.Fatalf("missing manifest = %q, %v; want a fingerprint and no error", missing, err)
	}
	// A directory where the manifest should be is an I/O error, not "missing".
	unreadable := filepath.Join(directory, "manifest.lock")
	if err := os.Mkdir(unreadable, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := fingerprintWithRevision("base", unreadable); err == nil {
		t.Fatal("unreadable manifest was silently treated as missing")
	}
}

func TestSourceAndLockModeFlags(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	// The source command renders the lock to stdout in every mode.
	for _, args := range [][]string{{"source", "--update"}, {"source", "--reinstall"}} {
		var output bytes.Buffer
		if err := Execute(args, &output, &bytes.Buffer{}); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(output.String(), "echo testing") {
			t.Fatalf("%v output = %q, want the inline script", args, output.String())
		}
	}

	// lock writes the lock file instead of rendering stdout.
	var stderr bytes.Buffer
	if err := Execute([]string{"lock", "--reinstall"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("lock --reinstall: %v (stderr %q)", err, stderr.String())
	}
	contents, err := os.ReadFile(filepath.Join(directory, "data", "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "echo testing") {
		t.Fatalf("lock file = %s, want the inline plugin locked", contents)
	}
}

func TestSourceFailsOnMissingConfig(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SHELF_CONFIG_FILE", filepath.Join(directory, "missing", "config.toml"))
	t.Setenv("SHELF_CONFIG_DIR", filepath.Join(directory, "missing"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"source"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("source succeeded with a missing config")
	}
}

func TestUpdateFailsOnMissingConfig(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SHELF_CONFIG_FILE", filepath.Join(directory, "missing", "config.toml"))
	t.Setenv("SHELF_CONFIG_DIR", filepath.Join(directory, "missing"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"update"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("update succeeded with a missing config")
	}
}

func TestUpdateInteractiveRejectsNonInteractive(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Execute([]string{"update", "--interactive", "--non-interactive"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "cannot be used with --non-interactive") {
		t.Fatalf("err = %v, want the non-interactive conflict error", err)
	}
}

func TestCleanInteractiveRejectsNonInteractive(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Execute([]string{"clean", "--interactive", "--non-interactive"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "cannot be used with --non-interactive") {
		t.Fatalf("err = %v, want the non-interactive conflict error", err)
	}
}

func TestRemoveBareRejectsMissingName(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var stdout, stderr bytes.Buffer
	err := Execute([]string{"remove"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "accepts 1 arg(s)") {
		t.Fatalf("err = %v, want an argument-count error", err)
	}
}

func TestRemoveInteractiveRejectsNameArgumentWithEnv(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.alpha]\ninline = \"echo a\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var stdout, stderr bytes.Buffer
	err := Execute([]string{"remove", "--interactive", "alpha"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined with --interactive") {
		t.Fatalf("err = %v, want the NAME conflict error", err)
	}
}

func TestReloadFailsOnMissingConfig(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SHELF_CONFIG_FILE", filepath.Join(directory, "missing", "config.toml"))
	t.Setenv("SHELF_CONFIG_DIR", filepath.Join(directory, "missing"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"reload"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("reload succeeded with a missing config")
	}
}

func TestSelectPluginsNoPluginsConfigured(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var stdout, stderr bytes.Buffer
	err := Execute([]string{"remove", "--interactive"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no plugins configured") {
		t.Fatalf("err = %v, want a no-plugins error", err)
	}
}

func TestSelectPluginsSurfacesLoadErrors(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"remove", "--interactive"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Fatalf("err = %v, want a validation error", err)
	}
}

func TestUpdateInteractivePickerErrorStopsUpdate(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	original := interactiveSelect
	interactiveSelect = func(_ []string, _ io.Writer) ([]string, error) {
		return nil, errors.New("picker failed")
	}
	t.Cleanup(func() { interactiveSelect = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"update", "--interactive"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "picker failed") {
		t.Fatalf("err = %v, want the picker error surfaced", err)
	}
}

func TestStatusReportsConfigAndLockErrors(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_SHELL", "")

	run := func() error {
		return Execute([]string{"status"}, &bytes.Buffer{}, &bytes.Buffer{})
	}
	valid := "shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"

	if err := run(); err == nil {
		t.Fatal("status accepted a missing config")
	}
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("status accepted a sourceless plugin")
	}
	if err := os.WriteFile(configFile, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(configDir, "plugins.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("status accepted an unreadable revision manifest")
	}
	if err := os.Remove(filepath.Join(configDir, "plugins.lock")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_SHELL", "fish")
	if err := run(); err == nil {
		t.Fatal("status accepted an unsupported shell")
	}
	t.Setenv("SHELF_SHELL", "")
	if err := run(); err == nil {
		t.Fatal("status accepted a missing lock file")
	}

	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := "config_fingerprint = \"stale\"\nshell = \"zsh\"\n\n[[plugins]]\n  name = \"test\"\n  inline = \"echo testing\"\n  files = []\n"
	if err := os.WriteFile(filepath.Join(dataDir, "plugins.lock"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("status err = %v, want a stale lockfile error", err)
	}
}

func TestDoctorReportsConfigAndLockErrors(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_SHELL", "")

	run := func() error {
		return Execute([]string{"doctor"}, &bytes.Buffer{}, &bytes.Buffer{})
	}
	valid := "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n"

	if err := run(); err == nil {
		t.Fatal("doctor accepted a missing config")
	}
	if err := os.WriteFile(configFile, []byte("shell = \"bash\"\n\n[plugins.test]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("doctor accepted a sourceless plugin")
	}
	if err := os.WriteFile(configFile, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(configDir, "plugins.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("doctor accepted an unreadable revision manifest")
	}
	if err := os.Remove(filepath.Join(configDir, "plugins.lock")); err != nil {
		t.Fatal(err)
	}

	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := "config_fingerprint = \"stale\"\nshell = \"bash\"\n\n[[plugins]]\n  name = \"test\"\n  inline = \"echo test\"\n  files = []\n"
	if err := os.WriteFile(filepath.Join(dataDir, "plugins.lock"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("doctor err = %v, want a stale lockfile error", err)
	}
	if err := os.Remove(filepath.Join(dataDir, "plugins.lock")); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("doctor accepted a missing lock file")
	}
}

func TestDoctorReportsUnavailableShellAndGit(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_SHELL", "")

	write := func(contents string) {
		t.Helper()
		if err := os.WriteFile(configFile, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func() error {
		return Execute([]string{"doctor"}, &bytes.Buffer{}, &bytes.Buffer{})
	}

	// A PATH without the configured shell surfaces the not-installed error.
	emptyBin := filepath.Join(directory, "empty-bin")
	if err := os.MkdirAll(emptyBin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", emptyBin)
	write("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n")
	if err := run(); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("doctor err = %v, want a not-installed shell error", err)
	}

	// A fake shell that fails `--version` surfaces the version error.
	fakeBin := filepath.Join(directory, "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeZsh := filepath.Join(fakeBin, "zsh")
	if err := os.WriteFile(fakeZsh, []byte("#!/bin/sh\necho fake zsh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBash := filepath.Join(fakeBin, "bash")
	bashScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"bash 5 test\"\n  exit 0\nfi\nexit 1\n"
	if err := os.WriteFile(fakeBash, []byte(bashScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	if err := run(); err == nil || !strings.Contains(err.Error(), "could not determine zsh version") {
		t.Fatalf("doctor err = %v, want a shell version error", err)
	}

	// A healthy shell combined with a Git plugin, but no git on PATH, reports git missing.
	write("shell = \"bash\"\n\n[plugins.test]\ngithub = \"owner/repo\"\n")
	if err := run(); err == nil || !strings.Contains(err.Error(), "git is required") {
		t.Fatalf("doctor err = %v, want a git-required error", err)
	}
}

func TestCleanReportsConfigErrors(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	run := func() error {
		return Execute([]string{"clean"}, &bytes.Buffer{}, &bytes.Buffer{})
	}
	if err := run(); err == nil {
		t.Fatal("clean accepted a missing config")
	}
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("clean accepted a sourceless plugin")
	}
}

func TestCleanInteractiveEmptySelection(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.current]\ninline = \"echo current\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "plugins", "obsolete"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	original := interactiveSelect
	interactiveSelect = func(_ []string, _ io.Writer) ([]string, error) {
		return []string{}, nil
	}
	t.Cleanup(func() { interactiveSelect = original })

	var stdout bytes.Buffer
	if err := Execute([]string{"clean", "--interactive"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "✓ nothing to clean\n" {
		t.Fatalf("clean output = %q, want nothing-to-clean", stdout.String())
	}
}

func TestCleanInteractivePickerError(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.current]\ninline = \"echo current\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "plugins", "obsolete"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	original := interactiveSelect
	interactiveSelect = func(_ []string, _ io.Writer) ([]string, error) {
		return nil, errors.New("picker failed")
	}
	t.Cleanup(func() { interactiveSelect = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"clean", "--interactive"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "picker failed") {
		t.Fatalf("err = %v, want the picker error surfaced", err)
	}
}

func TestVerboseLockReportsRemovedSources(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.current]\ninline = \"echo current\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Inline plugins live in the lock, so this install directory is unowned.
	if err := os.MkdirAll(filepath.Join(dataDir, "plugins", "obsolete"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	var stderr bytes.Buffer
	if err := Execute([]string{"lock", "--verbose"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Removed plugins/obsolete") {
		t.Fatalf("verbose lock stderr = %q, want a Removed status", stderr.String())
	}
}

func TestLockRejectsMissingRequiredLocalSource(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	config := "shell = \"zsh\"\n\n[plugins.missing]\nlocal = \"" + filepath.Join(directory, "gone") + "\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var stderr bytes.Buffer
	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &stderr); err == nil {
		t.Fatal("lock accepted a missing required local source")
	}
}

func TestInitConfirmPromptErrorStopsInit(t *testing.T) {
	_, _, _ = initTestEnv(t)
	originalShell := initShellPrompt
	originalConfirm := initConfirmPrompt
	initShellPrompt = func(_ *bufio.Reader, _ io.Writer) (config.Shell, error) {
		return config.Bash, nil
	}
	initConfirmPrompt = func(_ string, _ *bufio.Reader, _ io.Writer) (bool, error) {
		return false, errors.New("stdin closed")
	}
	t.Cleanup(func() {
		initShellPrompt = originalShell
		initConfirmPrompt = originalConfirm
	})

	if err := Execute([]string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "stdin closed") {
		t.Fatalf("err = %v, want a stdin-closed error", err)
	}
}

func TestSelfUpdateQuietSkipsDiagnostics(t *testing.T) {
	t.Setenv("SHELF_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(t.TempDir(), "data"))

	original := runUpdate
	runUpdate = func(_ context.Context, options selfupdate.Options) (selfupdate.Result, error) {
		if options.Diagnostics != nil {
			t.Error("quiet self-update retained diagnostics")
		}
		return selfupdate.Result{Updated: false, Next: "1.0.0"}, nil
	}
	t.Cleanup(func() { runUpdate = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"self-update", "--quiet"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestSelfUpdateSurfacesUpdateErrors(t *testing.T) {
	t.Setenv("SHELF_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(t.TempDir(), "data"))

	original := runUpdate
	runUpdate = func(_ context.Context, _ selfupdate.Options) (selfupdate.Result, error) {
		return selfupdate.Result{}, errors.New("download failed")
	}
	t.Cleanup(func() { runUpdate = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"self-update"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "download failed") {
		t.Fatalf("err = %v, want the download error surfaced", err)
	}
}

func TestCommandsRejectAConfigFileAsDirectory(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.WriteFile(configDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", filepath.Join(directory, "config.toml"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	var stderr bytes.Buffer
	if err := Execute([]string{"list"}, &bytes.Buffer{}, &stderr); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("list err = %v, want a not-a-directory error", err)
	}
}

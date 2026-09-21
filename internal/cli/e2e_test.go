package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"shelf/internal/lock"
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

func TestUpdateEmitsSourceWithoutWritingLockfile(t *testing.T) {
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
	if _, err := os.Stat(filepath.Join(directory, "data", "plugins.lock")); !os.IsNotExist(err) {
		t.Fatalf("update wrote lock file: %v", err)
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
	configFile := filepath.Join(configDir, "plugins.toml")
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
	configFile := filepath.Join(configDir, "plugins.toml")
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
	configFile := filepath.Join(configDir, "plugins.toml")
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
	configFile := filepath.Join(configDir, "plugins.toml")
	config := "shell = \"zsh\"\n\nplugins.fzf.inline = \"echo fzf\"\n\n[plugins.kept]\ninline = \"echo kept\"\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"remove", "fzf"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
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
	configFile := filepath.Join(configDir, "plugins.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	if err := Execute([]string{"add", "private", "--github", "rubiin/repository", "--proto", "ssh"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
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
	configFile := filepath.Join(configDir, "plugins.toml")
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
	configFile := filepath.Join(configDir, "plugins.toml")
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
	configFile := filepath.Join(configDir, "plugins.toml")
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
	// Drift the second checkout so the two git checks disagree; both still report in
	// declaration order even though they run concurrently.
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
	if output.String() != "removed: plugins/current\nremoved: plugins/obsolete\n" {
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
	configFile := filepath.Join(configDir, "plugins.toml")
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
		"removed: plugins/obsolete\n"
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
	configFile := filepath.Join(configDir, "plugins.toml")
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
	configFile := filepath.Join(configDir, "plugins.toml")
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
	t.Setenv("SHELF_CONFIG_FILE", filepath.Join(directory, "absent", "plugins.toml"))
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
	configFile := filepath.Join(configDir, "plugins.toml")
	contents := []byte("shell = zsh\n\n[plugins.demo\ninline = \"echo demo\"\n")
	if err := os.WriteFile(configFile, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(directory, "data")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", dataDir)

	locked := lock.LockedConfig{
		ConfigFingerprint: fingerprintWithRevision(fingerprintWithShell(contents), filepath.Join(configDir, "plugins.lock")),
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
	// SHELF_SHELL only matters when the config omits `shell`, and the lock remembers which shell
	// built it, so dropping the override has to relock rather than keep rendering bash code.
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "plugins.toml")
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

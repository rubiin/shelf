package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"shelf/internal/config"
	"shelf/internal/lock"
	"shelf/internal/selfupdate"
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
	// The default `completion` command is registered by Cobra when executing,
	// so initialize it before inspecting the command surface.
	root.InitDefaultCompletionCmd()
	if root.Use != "shelf" {
		t.Fatalf("root use = %q", root.Use)
	}
	commands := map[string]bool{}
	for _, command := range root.Commands() {
		commands[command.Name()] = true
	}
	for _, name := range []string{"init", "lock", "source", "update", "path", "status", "doctor", "clean", "list", "info", "add", "edit", "remove", "completion", "self-update"} {
		if !commands[name] {
			t.Errorf("root command %q is missing", name)
		}
		command, _, err := root.Find([]string{name})
		if err != nil || command.Short == "" {
			t.Errorf("root command %q has no description", name)
		}
	}
	add, _, err := root.Find([]string{"add"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dir", "file", "proto", "apply", "profiles", "hooks", "frozen", "ignore"} {
		if add.Flags().Lookup(name) == nil {
			t.Errorf("add flag %q is missing", name)
		}
	}
	selfUpdate, _, err := root.Find([]string{"self-update"})
	if err != nil {
		t.Fatal(err)
	}
	if selfUpdate.Flags().Lookup("force") == nil {
		t.Error("self-update flag --force is missing")
	}
	for _, name := range []string{"lock", "source", "update"} {
		command, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		flag := command.Flags().Lookup("concurrency")
		if flag == nil || flag.DefValue != "8" {
			t.Errorf("%s concurrency flag = %+v", name, flag)
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

func TestConfigShellSelection(t *testing.T) {
	t.Setenv("SHELF_SHELL", "")
	shell, err := configShell()
	if err != nil || shell != config.Zsh {
		t.Fatalf("default shell = %q, err = %v, want %q", shell, err, config.Zsh)
	}

	t.Setenv("SHELF_SHELL", "bash")
	shell, err = configShell()
	if err != nil || shell != config.Bash {
		t.Fatalf("explicit shell = %q, err = %v, want %q", shell, err, config.Bash)
	}

	t.Setenv("SHELF_SHELL", "fish")
	if _, err := configShell(); err == nil || !strings.Contains(err.Error(), "SHELF_SHELL") {
		t.Fatalf("unknown shell err = %v, want an error naming SHELF_SHELL", err)
	}
}

func TestLockRejectsUnknownShellEnvironment(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("[plugins.test]\ninline = \"echo hi\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", directory)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_SHELL", "fish")

	if err := Execute([]string{"lock"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "SHELF_SHELL") {
		t.Fatalf("lock err = %v, want an error naming SHELF_SHELL", err)
	}
}

func TestAddRejectsTOMLHostilePluginNames(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", directory)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))

	for _, name := range []string{"bad name", "my.plugin", `quo"te`} {
		if err := Execute([]string{"add", name, "--inline", "echo hi"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Errorf("add accepted plugin name %q", name)
		}
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "plugins") {
		t.Fatalf("rejected names still wrote sections: %s", contents)
	}
}

func TestSplitEditorCommand(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "plain", value: "vim", want: []string{"vim"}},
		{name: "flags", value: "nvim --wait", want: []string{"nvim", "--wait"}},
		{name: "extra spaces", value: "  vim   -f  ", want: []string{"vim", "-f"}},
		{name: "quoted path with spaces", value: `'/opt/my editor/nvim' --wait`, want: []string{"/opt/my editor/nvim", "--wait"}},
		{name: "double quoted path", value: `"/opt/my editor/nvim"`, want: []string{"/opt/my editor/nvim"}},
		{name: "quoted argument", value: `vim -c "set nobackup"`, want: []string{"vim", "-c", "set nobackup"}},
		{name: "escaped spaces", value: `/opt/my\ editor/vim -f`, want: []string{"/opt/my editor/vim", "-f"}},
		{name: "empty", value: "", want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := splitEditorCommand(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("arguments = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSplitEditorCommandRejectsUnbalancedQuotes(t *testing.T) {
	if _, err := splitEditorCommand(`'/opt/my editor/nvim`); err == nil {
		t.Fatal("unbalanced quote was accepted")
	}
}

func TestEditRunsEditorFromQuotedPath(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	editorDirectory := filepath.Join(directory, "my editor")
	if err := os.MkdirAll(editorDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(editorDirectory, "editor.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SHELF_EDITOR_CAPTURE\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(directory, "editor-args")
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_EDITOR", "'"+editor+"' --wait")
	t.Setenv("SHELF_EDITOR_CAPTURE", capture)

	var stdout bytes.Buffer
	if err := Execute([]string{"edit"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "✓ edited: "+configFile+"\n" {
		t.Fatalf("edit output = %q, want ✓ edited: %s", stdout.String(), configFile)
	}
	contents, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	want := "--wait\n" + configFile + "\n"
	if string(contents) != want {
		t.Fatalf("editor arguments = %q, want %q", contents, want)
	}
}

func TestSelfUpdateReportsUpToDate(t *testing.T) {
	t.Setenv("SHELF_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(t.TempDir(), "data"))

	original := runUpdate
	runUpdate = func(_ context.Context, options selfupdate.Options) (selfupdate.Result, error) {
		if options.CurrentVersion != Version {
			t.Errorf("current version = %q, want the stamped version %q", options.CurrentVersion, Version)
		}
		if options.Diagnostics == nil {
			t.Error("self-update ran without diagnostics")
		}
		return selfupdate.Result{Updated: false, Next: "9.9.9"}, nil
	}
	t.Cleanup(func() { runUpdate = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"self-update"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "✓ shelf 9.9.9 is up to date\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSelfUpdateReportsTheInstalledVersion(t *testing.T) {
	t.Setenv("SHELF_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(t.TempDir(), "data"))

	original := runUpdate
	runUpdate = func(_ context.Context, _ selfupdate.Options) (selfupdate.Result, error) {
		return selfupdate.Result{Updated: true, Previous: "1.0.0", Next: "2.0.0"}, nil
	}
	t.Cleanup(func() { runUpdate = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"self-update", "--force"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "✓ updated shelf: 1.0.0 -> 2.0.0\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSelfUpdatePassesForce(t *testing.T) {
	t.Setenv("SHELF_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	t.Setenv("SHELF_DATA_DIR", filepath.Join(t.TempDir(), "data"))

	original := runUpdate
	runUpdate = func(_ context.Context, options selfupdate.Options) (selfupdate.Result, error) {
		if !options.Force {
			t.Error("--force was not forwarded")
		}
		return selfupdate.Result{Updated: false, Next: "1.0.0"}, nil
	}
	t.Cleanup(func() { runUpdate = original })

	var stdout, stderr bytes.Buffer
	if err := Execute([]string{"self-update", "--force"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestUsesGitCoversEveryForge(t *testing.T) {
	for name, plugin := range map[string]config.RawPlugin{
		"git": {Git: "https://example.com/repo"}, "github": {GitHub: "owner/repo"}, "gist": {Gist: "id"},
		"gitlab": {GitLab: "owner/repo"}, "bitbucket": {Bitbucket: "owner/repo"}, "codeberg": {Codeberg: "owner/repo"},
	} {
		if !usesGit(config.Config{Plugins: map[string]config.RawPlugin{name: plugin}}) {
			t.Errorf("usesGit ignored a %s-only plugin", name)
		}
	}
	if usesGit(config.Config{Plugins: map[string]config.RawPlugin{"local": {Local: "/tmp"}}}) {
		t.Error("usesGit reported git for a local-only config")
	}
}

// withCleanProfile pins the package-level profile so direct calls to profile-aware
// helpers are deterministic regardless of what earlier cobra executions left behind.
func withCleanProfile(t *testing.T) {
	t.Helper()
	original := profile
	profile = ""
	t.Cleanup(func() { profile = original })
}

func TestRuntimeContextResolvesSettings(t *testing.T) {
	directory := t.TempDir()
	wantConfigDir := filepath.Join(directory, "config")
	wantConfigFile := filepath.Join(wantConfigDir, "config.toml")
	wantDataDir := filepath.Join(directory, "data")

	originalProfile, originalColor := profile, color
	originalQuiet, originalNonInteractive, originalVerbose := quiet, nonInteractive, verbose
	originalConfigDir, originalDataDir, originalConfigFile := configDir, dataDir, configFile
	t.Cleanup(func() {
		profile, color = originalProfile, originalColor
		quiet, nonInteractive, verbose = originalQuiet, originalNonInteractive, originalVerbose
		configDir, dataDir, configFile = originalConfigDir, originalDataDir, originalConfigFile
	})
	profile, color = "work", "always"
	quiet, nonInteractive, verbose = true, true, true
	configDir, dataDir, configFile = wantConfigDir, wantDataDir, wantConfigFile

	context := RuntimeContext()
	if context.Profile != "work" || context.Color != "always" {
		t.Fatalf("context settings = %+v", context)
	}
	if context.ConfigFile != wantConfigFile || context.ConfigDirectory != wantConfigDir {
		t.Fatalf("context paths = config %q dir %q, want %q / %q", context.ConfigFile, context.ConfigDirectory, wantConfigFile, wantConfigDir)
	}
	if context.DataDirectory != wantDataDir {
		t.Fatalf("context data directory = %q, want %q", context.DataDirectory, wantDataDir)
	}

	// An unresolvable home leaves every path field empty instead of panicking.
	t.Setenv("HOME", "")
	context = RuntimeContext()
	if context.ConfigFile != "" || context.ConfigDirectory != "" || context.DataDirectory != "" {
		t.Fatalf("unresolvable home left path fields populated: %+v", context)
	}
}

func TestHomeDirUsesHOMEThenUserHomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := homeDir(); got != home {
		t.Fatalf("homeDir = %q, want the HOME value %q", got, home)
	}

	// With HOME unset the os.UserHomeDir fallback runs without crashing; its
	// value is environment-dependent, so only that the branch re-ran is asserted.
	t.Setenv("HOME", "")
	if got := homeDir(); got == home {
		t.Fatalf("homeDir returned the prior HOME %q with HOME unset", got)
	}
}

func TestConfigShellAcceptsExplicitZsh(t *testing.T) {
	t.Setenv("SHELF_SHELL", "zsh")
	if shell, err := configShell(); err != nil || shell != config.Zsh {
		t.Fatalf("shell = %q, err = %v, want explicit zsh", shell, err)
	}
}

func TestUnlockedLockRejectsUnreadablePaths(t *testing.T) {
	withCleanProfile(t)
	t.Setenv("SHELF_SHELL", "")
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	lockPath := filepath.Join(dataDir, "plugins.lock")
	paths := Paths{ConfigDirectory: configDir, DataDirectory: dataDir, ConfigFile: configFile}

	// An unreadable config must take the slow path, never render a stale lock.
	if _, valid := unlockedLock(paths, lockPath); valid {
		t.Fatal("unlockedLock accepted a missing config")
	}
	contents := []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n")
	if err := os.WriteFile(configFile, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	// A missing lock file is equally invalid.
	if _, valid := unlockedLock(paths, lockPath); valid {
		t.Fatal("unlockedLock accepted a missing lock file")
	}
	// A healthy config and lock verify, so only the manifest error stays.
	fingerprint, err := fingerprintWithRevision(fingerprintWithShell(contents), paths.RevisionLockFile(""))
	if err != nil {
		t.Fatal(err)
	}
	locked := lock.LockedConfig{ConfigFingerprint: fingerprint, Shell: "zsh", Templates: map[string]string{"source": "source \"{{ file }}\""}, Plugins: []lock.LockedPlugin{{Name: "test", Inline: "echo testing"}}}
	if err := lock.Write(lockPath, locked); err != nil {
		t.Fatal(err)
	}
	if _, valid := unlockedLock(paths, lockPath); !valid {
		t.Fatal("unlockedLock rejected a matching config and lock")
	}
	// An unreadable revision manifest invalidates the fast path instead of silently dropping pins.
	if err := os.MkdirAll(paths.RevisionLockFile(""), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, valid := unlockedLock(paths, lockPath); valid {
		t.Fatal("unlockedLock ignored an unreadable revision manifest")
	}
}

func TestPluginSourceRendersEveryKind(t *testing.T) {
	tests := []struct {
		name   string
		plugin config.RawPlugin
		want   string
	}{
		{"github", config.RawPlugin{GitHub: "owner/repo"}, "https://github.com/owner/repo"},
		{"git", config.RawPlugin{Git: "https://example.com/repo.git"}, "https://example.com/repo.git"},
		{"gist", config.RawPlugin{Gist: "deadbeef"}, "https://gist.github.com/deadbeef"},
		{"remote", config.RawPlugin{Remote: "https://example.com/plugin.zsh"}, "https://example.com/plugin.zsh"},
		{"local", config.RawPlugin{Local: "/tmp/plugin"}, "/tmp/plugin"},
		{"inline", config.RawPlugin{Inline: "echo hi"}, "inline"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := pluginSource(test.plugin); got != test.want {
				t.Fatalf("pluginSource(%+v) = %q, want %q", test.plugin, got, test.want)
			}
		})
	}
}

func TestDisplayPathShortensHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inside := filepath.Join(home, "config", "config.toml")
	want := "~" + string(filepath.Separator) + "config" + string(filepath.Separator) + "config.toml"
	if got := displayPath(inside); got != want {
		t.Fatalf("displayPath inside home = %q, want %q", got, want)
	}
	outside := filepath.Join(t.TempDir(), "shared", "config.toml")
	if got := displayPath(outside); got != outside {
		t.Fatalf("displayPath outside home = %q, want the path unchanged", got)
	}
}

func TestEditConfigRejectsMissingEditor(t *testing.T) {
	t.Setenv("SHELF_EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if err := editConfig(Paths{}); err == nil || !strings.Contains(err.Error(), "no editor") {
		t.Fatalf("editConfig err = %v, want a no-editor error", err)
	}
}

func TestEditConfigRejectsAllBlankEditors(t *testing.T) {
	for name, value := range map[string]string{
		"SHELF_EDITOR": "   ",
		"VISUAL":       "   ",
		"EDITOR":       "   ",
	} {
		t.Setenv("SHELF_EDITOR", "")
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "")
		t.Setenv(name, value)
		if err := editConfig(Paths{}); err == nil || !strings.Contains(err.Error(), "no editor") {
			t.Fatalf("%s=%q err = %v, want a no-editor error", name, value, err)
		}
	}
}

func TestEditConfigRejectsUnbalancedEditorQuotes(t *testing.T) {
	t.Setenv("SHELF_EDITOR", "'/opt/my editor")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if err := editConfig(Paths{}); err == nil || !strings.Contains(err.Error(), "unbalanced quotes") {
		t.Fatalf("editConfig err = %v, want an unbalanced-quotes error", err)
	}
}

func TestListPluginsReportsLoadErrors(t *testing.T) {
	if err := listPlugins(Paths{ConfigFile: filepath.Join(t.TempDir(), "missing.toml")}, io.Discard); err == nil {
		t.Fatal("listPlugins accepted a missing config")
	}
}

func TestListPluginsSurfacesWriteErrors(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.a]\ninline = \"echo a\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := listPlugins(Paths{ConfigFile: configFile}, errWriter{}); err == nil {
		t.Fatal("listPlugins swallowed a write error")
	}
}

func TestPrintPathsSurfacesWriteErrors(t *testing.T) {
	paths := Paths{ConfigDirectory: "/c", DataDirectory: "/d", ConfigFile: "/c/config.toml"}
	if err := printPaths(paths, errWriter{}); err == nil {
		t.Fatal("printPaths swallowed a write error")
	}
}

func TestPluginInfoRejectsMissingLockFile(t *testing.T) {
	withCleanProfile(t)
	directory := t.TempDir()
	paths := Paths{DataDirectory: directory, ConfigFile: filepath.Join(directory, "config.toml")}
	if err := pluginInfo(paths, "demo", io.Discard); err == nil {
		t.Fatal("pluginInfo accepted a missing lock file")
	}
}

func TestPluginInfoRejectsUnmeasurablePluginSize(t *testing.T) {
	withCleanProfile(t)
	directory := t.TempDir()
	lockPath := filepath.Join(directory, "plugins.lock")
	locked := lock.LockedConfig{Shell: "zsh", Plugins: []lock.LockedPlugin{{Name: "demo", Directory: filepath.Join(directory, "gone")}}}
	if err := lock.Write(lockPath, locked); err != nil {
		t.Fatal(err)
	}
	paths := Paths{DataDirectory: directory, ConfigFile: filepath.Join(directory, "config.toml")}
	if err := pluginInfo(paths, "demo", io.Discard); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("pluginInfo err = %v, want a size error for a vanished directory", err)
	}
}

func TestLoadSourceInputsErrorPaths(t *testing.T) {
	withCleanProfile(t)
	t.Setenv("SHELF_SHELL", "")
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := Paths{ConfigDirectory: configDir, DataDirectory: dataDir, ConfigFile: configFile}
	valid := "shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo testing\"\n"

	if _, err := loadSourceInputs(paths, io.Discard); err == nil {
		t.Fatal("loadSourceInputs accepted a missing config")
	}
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSourceInputs(paths, io.Discard); err == nil {
		t.Fatal("loadSourceInputs accepted a sourceless plugin")
	}
	if err := os.WriteFile(configFile, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.RevisionLockFile(""), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSourceInputs(paths, io.Discard); err == nil {
		t.Fatal("loadSourceInputs ignored an unreadable revision manifest")
	}
	if err := os.Remove(paths.RevisionLockFile("")); err != nil {
		t.Fatal(err)
	}
	// A config without a shell falls through to SHELF_SHELL, which must be
	// a supported shell.
	if err := os.WriteFile(configFile, []byte("[plugins.test]\ninline = \"echo testing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_SHELL", "fish")
	if _, err := loadSourceInputs(paths, io.Discard); err == nil {
		t.Fatal("loadSourceInputs accepted an unsupported shell")
	}
}

func TestApplyRevisionManifestRejectsUnreadableManifest(t *testing.T) {
	withCleanProfile(t)
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "plugins.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := Paths{ConfigDirectory: directory}
	if _, err := applyRevisionManifest(paths, config.Config{}, lock.ModeNormal); err == nil {
		t.Fatal("applyRevisionManifest ignored an unreadable manifest")
	}
}

func TestApplyRevisionManifestSkipsMissingManifest(t *testing.T) {
	withCleanProfile(t)
	paths := Paths{ConfigDirectory: t.TempDir()}
	cfg := config.Config{Shell: config.Bash}
	got, err := applyRevisionManifest(paths, cfg, lock.ModeNormal)
	if err != nil || got.Shell != cfg.Shell {
		t.Fatalf("applyRevisionManifest = %+v, %v; want the config unchanged", got, err)
	}
}

func TestInitConfigRejectsUnstatableConfigPath(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	paths := Paths{ConfigDirectory: t.TempDir(), DataDirectory: filepath.Join(t.TempDir(), "data"), ConfigFile: filepath.Join(blocker, "config.toml")}
	if err := initConfig(command, paths, ""); err == nil {
		t.Fatal("initConfig accepted a config path under a file")
	}
}

func TestInitShellPromptAcceptsAChoice(t *testing.T) {
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("bash\n"))
	shell, err := initShellPrompt(in, &out)
	if err != nil || shell != config.Bash {
		t.Fatalf("shell = %q, err = %v, want bash", shell, err)
	}
}

func TestInitConfirmPromptAcceptsYes(t *testing.T) {
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("y\n"))
	ok, err := initConfirmPrompt("/tmp/config.toml", in, &out)
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v, want true", ok, err)
	}
}

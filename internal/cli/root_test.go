package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"shelf/internal/config"
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
	if root.Use != "shelf" {
		t.Fatalf("root use = %q", root.Use)
	}
	commands := map[string]bool{}
	for _, command := range root.Commands() {
		commands[command.Name()] = true
	}
	for _, name := range []string{"init", "lock", "source", "update", "path", "status", "doctor", "clean", "list", "add", "edit", "remove", "completions", "self-update"} {
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
	for _, name := range []string{"dir", "file", "proto", "apply", "profiles", "hooks"} {
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

	if err := Execute([]string{"edit"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
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

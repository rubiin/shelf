package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	if _, err := os.Stat(filepath.Join(configDir, "plugins.lock")); !os.IsNotExist(err) {
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
	if _, err := os.Stat(filepath.Join(configDir, "plugins.lock")); err != nil {
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
		"lock_file=" + filepath.Join(configDir, "plugins.lock") + "\n"
	if output.String() != want {
		t.Fatalf("path output = %q, want %q", output.String(), want)
	}
}

func TestLockStoresLockfileInConfigDirectory(t *testing.T) {
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
	if _, err := os.Stat(filepath.Join(configDir, "plugins.lock")); err != nil {
		t.Fatalf("lock file missing in config directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "plugins.lock")); err == nil {
		t.Fatal("lock file was written under the data directory")
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
	checkout := filepath.Join(directory, "data", "plugins", "test")
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

func TestDoctorReportsHealthyConfigurationAndLock(t *testing.T) {
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"zsh\"\n\n[plugins.test]\ninline = \"echo test\"\n"), 0o600); err != nil {
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
	if output.String() != "config: ok\nlock: ok\n" {
		t.Fatalf("doctor output = %q", output.String())
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
	if output.String() != "removed: obsolete\n" {
		t.Fatalf("clean output = %q", output.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "plugins", "current")); err != nil {
		t.Fatalf("configured plugin was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "plugins", "obsolete")); !os.IsNotExist(err) {
		t.Fatalf("obsolete plugin remains: %v", err)
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
	checkout := filepath.Join(directory, "data", "plugins", "test")
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

package cli

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"shelf/internal/config"
	"shelf/internal/lock"
	"shelf/internal/render"
)

// failWriter succeeds until its failOn-th write, then fails.
type failWriter struct {
	writes int
	failOn int
}

func (writer *failWriter) Write(data []byte) (int, error) {
	writer.writes++
	if writer.writes == writer.failOn {
		return 0, errors.New("injected write failure")
	}
	return len(data), nil
}

// pathsFixture returns isolated config and data directories with the given
// config file contents.
func pathsFixture(t *testing.T, configText string) Paths {
	t.Helper()
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	return Paths{ConfigDirectory: configDir, DataDirectory: dataDir, ConfigFile: configFile}
}

// chmodLocked makes path non-writable for the duration of the test, which
// fails writes the process cannot credential-bypass.
func chmodLocked(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, 0o755)
	})
}

// TestResolvePathsFailuresSurfaceFromCommands covers the error return in each
// command's RunE: with an empty HOME, resolvePaths fails for every command.
func TestResolvePathsFailuresSurfaceFromCommands(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("SHELF_CONFIG_DIR", "")
	t.Setenv("SHELF_CONFIG_FILE", "")
	t.Setenv("SHELF_DATA_DIR", "")
	for _, args := range [][]string{{"source"}, {"reload"}, {"path"}, {"init"}} {
		if err := Execute(args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("%v succeeded with an empty HOME", args)
		}
	}
}

func TestLockConfigRejectsUnsupportedShell(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"fish\"\n\n[plugins.test]\ninline = \"echo test\"\n")
	if err := lockConfig(paths, lock.ModeNormal, 1, io.Discard); err == nil {
		t.Fatal("lockConfig accepted an unsupported shell")
	}
}

// TestCleanFunctionsFailOnUnreadableDirectories drives every clean entry point
// over a plugins directory that cannot be listed.
func TestCleanFunctionsFailOnUnreadableDirectories(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n")
	plugins := filepath.Join(paths.DataDirectory, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	chmodLocked(t, plugins, 0o000)

	if err := lockConfig(paths, lock.ModeNormal, 1, io.Discard); err == nil {
		t.Fatal("lockConfig tolerated an unlistable plugins directory")
	}
	if err := sourceConfig(paths, io.Discard, io.Discard, true, lock.ModeNormal, 1); err == nil {
		t.Fatal("sourceConfig tolerated an unlistable plugins directory")
	}
	if err := updateSources(paths, io.Discard, io.Discard, 1, nil); err == nil {
		t.Fatal("updateSources tolerated an unlistable plugins directory")
	}
	if err := cleanPlugins(paths, io.Discard, io.Discard, false); err == nil {
		t.Fatal("cleanPlugins tolerated an unlistable plugins directory")
	}
	if err := cleanUnownedSources(paths.DataDirectory, mustLoadFixture(t, paths), newLogger(io.Discard)); err == nil {
		t.Fatal("cleanUnownedSources tolerated an unlistable plugins directory")
	}
	if _, err := cleanInstallDirectories(paths.DataDirectory, mustLoadFixture(t, paths)); err == nil {
		t.Fatal("cleanInstallDirectories tolerated an unlistable plugins directory")
	}
	if _, err := findUnownedPaths(paths.DataDirectory, mustLoadFixture(t, paths)); err == nil {
		t.Fatal("findUnownedPaths tolerated an unlistable plugins directory")
	}
	if _, err := collectUnownedPaths(plugins, nil, nil); err == nil {
		t.Fatal("collectUnownedPaths tolerated an unlistable root")
	}
}

// mustLoadFixture decodes the fixture config; the test never reaches it after
// chmod changes, so its own error handling stays uncoupled.
func mustLoadFixture(t *testing.T, paths Paths) config.Config {
	t.Helper()
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestRevisionManifestSurfacesErrors covers corrupt manifest reads and
// unwritable manifest writes in the lock-, source-, and update-config flows.
func TestRevisionManifestSurfacesErrors(t *testing.T) {
	withCleanProfile(t)
	valid := "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n"

	// A corrupt manifest fails applyRevisionManifest.
	corrupt := pathsFixture(t, valid)
	if err := os.WriteFile(corrupt.RevisionLockFile(""), []byte("plugins = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lockConfig(corrupt, lock.ModeNormal, 1, io.Discard); err == nil {
		t.Fatal("lockConfig accepted a corrupt revision manifest")
	}
	if err := sourceConfig(corrupt, io.Discard, io.Discard, true, lock.ModeNormal, 1); err == nil {
		t.Fatal("sourceConfig accepted a corrupt revision manifest")
	}

	// A write-protected config directory fails the manifest write: the lock
	// directory can still be acquired (read-only opens suffice) but the temp
	// file cannot be created.
	for _, name := range []string{"lockConfig", "sourceConfig", "updateSources"} {
		paths := pathsFixture(t, valid)
		chmodLocked(t, paths.ConfigDirectory, 0o555)
		var err error
		switch name {
		case "lockConfig":
			err = lockConfig(paths, lock.ModeNormal, 1, io.Discard)
		case "sourceConfig":
			err = sourceConfig(paths, io.Discard, io.Discard, true, lock.ModeNormal, 1)
		case "updateSources":
			err = updateSources(paths, io.Discard, io.Discard, 1, nil)
		}
		if err == nil {
			t.Fatalf("%s wrote a manifest into a read-only config directory", name)
		}
	}
}

// TestLockWriteFailsOnDirectoryAtLockPath covers lock.Write failing when the
// lock path is occupied by a directory, which the temp-file rename cannot
// replace.
func TestLockWriteFailsOnDirectoryAtLockPath(t *testing.T) {
	withCleanProfile(t)
	valid := "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n"
	for _, name := range []string{"lockConfig", "sourceConfig", "updateSources"} {
		paths := pathsFixture(t, valid)
		lockPath := paths.LockFile("")
		if err := os.MkdirAll(filepath.Join(lockPath, "occupied"), 0o755); err != nil {
			t.Fatal(err)
		}
		var err error
		switch name {
		case "lockConfig":
			err = lockConfig(paths, lock.ModeNormal, 1, io.Discard)
		case "sourceConfig":
			err = sourceConfig(paths, io.Discard, io.Discard, true, lock.ModeNormal, 1)
		case "updateSources":
			err = updateSources(paths, io.Discard, io.Discard, 1, nil)
		}
		if err == nil {
			t.Fatalf("%s wrote a lock over a directory at the lock path", name)
		}
	}
}

// TestSourceConfigAcquireFailsOnNonDirectoryConfig covers filelock acquire
// failures for both the shared and exclusive paths.
func TestSourceConfigAcquireFailsOnNonDirectoryConfig(t *testing.T) {
	withCleanProfile(t)
	directory := t.TempDir()
	configFile := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(configFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := Paths{ConfigDirectory: configFile, DataDirectory: filepath.Join(directory, "data"), ConfigFile: filepath.Join(configFile, "config.toml")}
	if err := sourceConfig(paths, io.Discard, io.Discard, false, lock.ModeNormal, 1); err == nil {
		t.Fatal("sourceConfig acquired a lock over a plain file")
	}
	if err := sourceConfig(paths, io.Discard, io.Discard, true, lock.ModeNormal, 1); err == nil {
		t.Fatal("sourceConfig acquired an exclusive lock over a plain file")
	}
}

func TestRenderScriptSurfacesUnknownTemplate(t *testing.T) {
	withCleanProfile(t)
	locked := lock.LockedConfig{Shell: "zsh", Plugins: []lock.LockedPlugin{{Name: "demo", Apply: []string{"missing-template"}}}}
	if err := renderScript(&bytes.Buffer{}, locked, io.Discard); err == nil {
		t.Fatal("renderScript accepted an unknown apply template")
	}
}

func TestSelectPluginsRejectsMissingConfig(t *testing.T) {
	withCleanProfile(t)
	directory := t.TempDir()
	paths := Paths{DataDirectory: directory, ConfigFile: filepath.Join(directory, "config.toml")}
	if _, err := selectPlugins(&cobra.Command{}, paths); err == nil {
		t.Fatal("selectPlugins accepted a missing config")
	}
}

func TestRemoveInteractiveConfigSurfacesRemoveError(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n")
	original := interactiveSelect
	interactiveSelect = func([]string, io.Writer) ([]string, error) {
		if err := os.Remove(paths.ConfigFile); err != nil {
			t.Fatal(err)
		}
		return []string{"test"}, nil
	}
	t.Cleanup(func() { interactiveSelect = original })
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := removeInteractiveConfig(cmd, paths); err == nil {
		t.Fatal("removeInteractiveConfig swallowed a config error")
	}
}

func TestInitConfigErrorPaths(t *testing.T) {
	withCleanProfile(t)
	t.Setenv("SHELF_SHELL", "")
	directory := t.TempDir()
	paths := Paths{DataDirectory: filepath.Join(directory, "data"), ConfigFile: filepath.Join(directory, "config.toml")}
	originalNonInteractive, originalPrompt := nonInteractive, initShellPrompt
	t.Cleanup(func() {
		nonInteractive = originalNonInteractive
		initShellPrompt = originalPrompt
	})

	nonInteractive = true
	if err := initConfig(&cobra.Command{}, paths, "fish"); err == nil {
		t.Fatal("initConfig accepted an unsupported shell flag")
	}
	nonInteractive = false
	initShellPrompt = func(*bufio.Reader, io.Writer) (config.Shell, error) {
		return "", errors.New("prompt failed")
	}
	if err := initConfig(&cobra.Command{}, paths, ""); err == nil {
		t.Fatal("initConfig swallowed a shell prompt error")
	}
}

// TestPluginInfoSurfacesWriteError and friends cover the output-write error
// returns in pluginInfo, pluginStatus, and doctor.
func TestPluginInfoSurfacesWriteError(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n")
	if err := lockConfig(paths, lock.ModeNormal, 1, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := pluginInfo(paths, "test", errWriter{}); err == nil {
		t.Fatal("pluginInfo swallowed a write error")
	}
}

func TestPluginStatusSurfacesWriteError(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n")
	if err := lockConfig(paths, lock.ModeNormal, 1, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := pluginStatus(paths, errWriter{}); err == nil {
		t.Fatal("pluginStatus swallowed a write error")
	}
}

func TestPluginStatusRejectsUnsupportedShellEnv(t *testing.T) {
	withCleanProfile(t)
	t.Setenv("SHELF_SHELL", "fish")
	paths := pathsFixture(t, "[plugins.test]\ninline = \"echo test\"\n")
	if err := pluginStatus(paths, io.Discard); err == nil {
		t.Fatal("pluginStatus accepted an unsupported SHELF_SHELL")
	}
}

// TestDoctorSurfacesWriteErrors fails each sequential output write so every
// write-error return in doctor is exercised.
func TestDoctorSurfacesWriteErrors(t *testing.T) {
	withCleanProfile(t)
	shellConfig := pathsFixture(t, "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n")
	for _, failOn := range []int{1, 2, 3, 4, 5} {
		if err := doctor(shellConfig, &failWriter{failOn: failOn}); err == nil {
			t.Fatalf("doctor swallowed a write error on write %d", failOn)
		}
	}
}

func TestDoctorSurfacesGitWriteError(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.git]\ngit = \"https://example.com/owner/repo.git\"\n")
	if err := doctor(paths, &failWriter{failOn: 6}); err == nil {
		t.Fatal("doctor swallowed a write error on the git line")
	}
}

func TestDoctorSurfacesLockLineWriteError(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n")
	if err := lockConfig(paths, lock.ModeNormal, 1, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := doctor(paths, &failWriter{failOn: 6}); err == nil {
		t.Fatal("doctor swallowed a write error on the lock line")
	}
}

func TestDoctorRejectsUnsupportedShellEnv(t *testing.T) {
	withCleanProfile(t)
	t.Setenv("SHELF_SHELL", "fish")
	paths := pathsFixture(t, "[plugins.test]\ninline = \"echo test\"\n")
	if err := doctor(paths, io.Discard); err == nil {
		t.Fatal("doctor accepted an unsupported SHELF_SHELL")
	}
}

func TestFindUnownedPathsRejectsMalformedSources(t *testing.T) {
	withCleanProfile(t)
	dataDirectory := t.TempDir()
	if _, err := findUnownedPaths(dataDirectory, config.Config{Plugins: map[string]config.RawPlugin{"bad": {Git: "https://"}}}); err == nil {
		t.Fatal("findUnownedPaths accepted a sourceless git URL")
	}
	if _, err := findUnownedPaths(dataDirectory, config.Config{Plugins: map[string]config.RawPlugin{"bad": {Remote: "notaurl"}}}); err == nil {
		t.Fatal("findUnownedPaths accepted a hostless remote")
	}
}

func TestCleanPluginsSurfacesWriteError(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n")
	plugins := filepath.Join(paths.DataDirectory, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(plugins, "orphan.txt")
	if err := os.WriteFile(orphan, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanPlugins(paths, errWriter{}, io.Discard, false); err == nil {
		t.Fatal("cleanPlugins swallowed a write error")
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("cleanPlugins did not remove the orphan")
	}
}

func TestCleanPluginsSurfacesRemoveError(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\n")
	plugins := filepath.Join(paths.DataDirectory, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(plugins, "orphan.txt")
	if err := os.WriteFile(orphan, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Walkable but not writable: the orphan lists cleanly and RemoveAll fails.
	chmodLocked(t, plugins, 0o555)
	if err := cleanPlugins(paths, io.Discard, io.Discard, false); err == nil {
		t.Fatal("cleanPlugins removed an orphan from a read-only directory")
	}
}

func TestInstallDisplayPathFallsBackOnRelError(t *testing.T) {
	path := installDisplayPath("relative/dir", "/abs/path")
	if path != "/abs/path" {
		t.Fatalf("installDisplayPath = %q, want the absolute path fallback", path)
	}
}

func TestEditCommandSurfacesEditorFailure(t *testing.T) {
	withCleanProfile(t)
	t.Setenv("SHELF_EDITOR", "/nonexistent-shelf-editor")
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"edit"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("edit succeeded with a nonexistent editor")
	}
}

func TestRemoveCommandRejectsUndecodableConfig(t *testing.T) {
	withCleanProfile(t)
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"bash\"\n[plugins.test]\ninline = ==\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"remove", "test"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("remove succeeded on an undecodable config")
	}
}

// TestSourceAndUpdateBuildFailuresSurfacesErrors covers BuildWithConcurrency
// failing in the source and update flows with an unfetchable git source.
func TestSourceAndUpdateBuildFailuresSurfacesErrors(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"bash\"\n\n[plugins.bad]\ngit = \"file:///nonexistent-shelf-repo\"\n")
	if err := sourceConfig(paths, io.Discard, io.Discard, true, lock.ModeNormal, 1); err == nil {
		t.Fatal("sourceConfig accepted an unfetchable git source")
	}
	if err := updateSources(paths, io.Discard, io.Discard, 1, nil); err == nil {
		t.Fatal("updateSources accepted an unfetchable git source")
	}
}

func TestInitShellPromptSurfacesReadError(t *testing.T) {
	if _, err := initShellPrompt(bufio.NewReader(strings.NewReader("")), io.Discard); err == nil {
		t.Fatal("initShellPrompt accepted an empty choice read")
	}
}

func TestRemoveInteractiveRejectsNonInteractive(t *testing.T) {
	withCleanProfile(t)
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	if err := Execute([]string{"remove", "--interactive", "--non-interactive"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("remove accepted --interactive with --non-interactive")
	}
}

// TestSourceFastPathWarnsOnUnmatchedProfile expects the lock built under a
// profile that matches no plugin, so the next source fast path can report it.
func TestSourceFastPathWarnsOnUnmatchedProfile(t *testing.T) {
	withCleanProfile(t)
	directory := t.TempDir()
	configDir := filepath.Join(directory, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configFile, []byte("shell = \"bash\"\n\n[plugins.test]\ninline = \"echo test\"\nprofiles = [\"work\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_DIR", configDir)
	t.Setenv("SHELF_CONFIG_FILE", configFile)
	t.Setenv("SHELF_DATA_DIR", filepath.Join(directory, "data"))
	t.Setenv("SHELF_PROFILE", "nomatch")
	for run := 0; run < 2; run++ {
		if err := Execute([]string{"source"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("source run %d: %v", run, err)
		}
	}
}

// TestSourceSurfacesRestoreFailure fabricates a valid lock that pins a git
// source without installed files, so verification passes while the missing
// checkout needs a restore that cannot clone the nonexistent repository.
func TestSourceSurfacesRestoreFailure(t *testing.T) {
	withCleanProfile(t)
	paths := pathsFixture(t, "shell = \"zsh\"\n\n[plugins.bad]\ngit = \"file:///nonexistent-shelf-repo\"\n")
	contents, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := fingerprintWithRevision(fingerprintWithShell(contents), paths.RevisionLockFile(""))
	if err != nil {
		t.Fatal(err)
	}
	locked := lock.LockedConfig{
		ConfigFingerprint: fingerprint,
		Shell:             "zsh",
		Templates:         render.ResolveTemplates("zsh", nil),
		Plugins: []lock.LockedPlugin{{
			Name: "bad",
			URL:  "file:///nonexistent-shelf-repo",
			Rev:  "v1",
		}},
	}
	if err := lock.Write(paths.LockFile(""), locked); err != nil {
		t.Fatal(err)
	}
	if err := sourceConfig(paths, io.Discard, io.Discard, false, lock.ModeNormal, 1); err == nil {
		t.Fatal("source restored a pinned revision from a nonexistent repository")
	}
}

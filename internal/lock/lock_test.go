package lock

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shelf/internal/config"
	"shelf/internal/source"
)

type testInstaller struct {
	directory string
	lastDir   *string
}

func (installer testInstaller) Install(_ context.Context, request source.Request) (source.Installed, error) {
	if installer.lastDir != nil {
		*installer.lastDir = request.Dir
	}
	return source.Installed{
		Directory: installer.directory,
		File:      filepath.Join(installer.directory, request.Name+".plugin.zsh"),
	}, nil
}

func TestWriteReplacesLockFileAtomically(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "plugins.lock")
	if err := os.WriteFile(path, []byte("stale = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := LockedConfig{ConfigFingerprint: "fingerprint", Shell: "zsh", Plugins: []LockedPlugin{{Name: "test", Source: "inline"}}}
	if err := Write(path, locked); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ConfigFingerprint != "fingerprint" || len(reloaded.Plugins) != 1 || reloaded.Plugins[0].Name != "test" {
		t.Fatalf("lock file = %+v", reloaded)
	}
	// The lock is written through a temporary file, so nothing else may be left behind.
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "plugins.lock" {
		t.Fatalf("directory entries = %v, want only plugins.lock", entries)
	}
}

func TestSuppliedFingerprintAvoidsReadingTheConfig(t *testing.T) {
	// The config path does not exist: supplying the fingerprint proves nothing is read.
	directory := t.TempDir()
	pluginDirectory := t.TempDir()
	file := filepath.Join(pluginDirectory, "test.plugin.zsh")
	if err := os.WriteFile(file, []byte("echo test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := Context{ConfigFile: filepath.Join(directory, "missing.toml"), ConfigFingerprint: "fingerprint", Shell: "zsh"}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{"test": {Inline: "echo hi"}}}
	locked, err := Build(ctx, cfg, testInstaller{directory: pluginDirectory}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if locked.ConfigFingerprint != "fingerprint" {
		t.Fatalf("fingerprint = %q, want the supplied value", locked.ConfigFingerprint)
	}
	lockPath := filepath.Join(directory, "plugins.lock")
	if err := Write(lockPath, locked); err != nil {
		t.Fatal(err)
	}
	valid, err := Verify(lockPath, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("lock file written with the supplied fingerprint did not verify")
	}
	// Without a fingerprint the fallback reads the missing config, so nothing can match.
	stale := Context{ConfigFile: ctx.ConfigFile, Shell: "zsh"}
	if valid, err := Verify(lockPath, stale); err != nil || valid {
		t.Fatalf("verify without a fingerprint = %v, err = %v", valid, err)
	}
}

func TestBuildRejectsNonPositiveConcurrency(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{"test": {Inline: "echo hi"}}}
	if _, err := BuildWithConcurrency(Context{Shell: "zsh"}, cfg, testInstaller{directory: t.TempDir()}, ModeNormal, 0); err == nil {
		t.Fatal("concurrency 0 was accepted")
	}
}

func TestRestoreInstallsRevisionsInParallel(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"first":  {GitHub: "example/first"},
		"second": {GitHub: "example/second"},
	}}
	locked := LockedConfig{Plugins: []LockedPlugin{
		{Name: "first", Rev: "aaaaaaaa"},
		{Name: "second", Rev: "bbbbbbbb"},
	}}
	installer := blockingInstaller{directory: t.TempDir(), started: make(chan struct{}, 2), release: make(chan struct{})}
	finished := make(chan error, 1)
	go func() { finished <- Restore(cfg, installer, locked, DefaultConcurrency) }()
	for range 2 {
		select {
		case <-installer.started:
		case <-time.After(time.Second):
			close(installer.release)
			t.Fatal("restores did not start in parallel")
		}
	}
	close(installer.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestRestoreSkipsPluginsWithoutPinnedRevisions(t *testing.T) {
	// A plugin without a pinned rev, or one no longer configured as Git, is skipped.
	cfg := config.Config{Plugins: map[string]config.RawPlugin{"local": {Local: "/tmp/plugins"}}}
	locked := LockedConfig{Plugins: []LockedPlugin{{Name: "local"}, {Name: "gone", Rev: "aaaaaaaa"}}}
	installer := &countingInstaller{}
	if err := Restore(cfg, installer, locked, DefaultConcurrency); err != nil {
		t.Fatal(err)
	}
	if installer.calls != 0 {
		t.Fatalf("installer called %d times, want 0", installer.calls)
	}
}

func TestBuildPreservesPluginDeclarationOrder(t *testing.T) {
	cfg := config.Config{
		Plugins: map[string]config.RawPlugin{
			"zsh-vi-mode": {Inline: "echo vi"},
			"zsh-defer":   {Inline: "echo defer"},
		},
		PluginOrder: []string{"zsh-defer", "zsh-vi-mode"},
	}

	locked, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{directory: t.TempDir()}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 2 || locked.Plugins[0].Name != "zsh-defer" || locked.Plugins[1].Name != "zsh-vi-mode" {
		t.Fatalf("plugin order = %v", []string{locked.Plugins[0].Name, locked.Plugins[1].Name})
	}
}

func TestBuildInstallsPluginsInParallel(t *testing.T) {
	cfg := config.Config{
		Plugins: map[string]config.RawPlugin{
			"first":  {Inline: "echo first"},
			"second": {Inline: "echo second"},
		},
		PluginOrder: []string{"first", "second"},
	}
	installer := blockingInstaller{directory: t.TempDir(), started: make(chan struct{}, 2), release: make(chan struct{})}
	type buildResult struct {
		locked LockedConfig
		err    error
	}
	finished := make(chan buildResult, 1)
	go func() {
		locked, err := Build(Context{Shell: "zsh"}, cfg, installer, ModeUpdate)
		finished <- buildResult{locked: locked, err: err}
	}()
	for range 2 {
		select {
		case <-installer.started:
		case <-time.After(time.Second):
			close(installer.release)
			t.Fatal("installs did not start in parallel")
		}
	}
	close(installer.release)
	outcome := <-finished
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if len(outcome.locked.Plugins) != 2 || outcome.locked.Plugins[0].Name != "first" || outcome.locked.Plugins[1].Name != "second" {
		t.Fatalf("plugin order = %+v", outcome.locked.Plugins)
	}
}

func TestBuildPassesPluginDirectoryToInstaller(t *testing.T) {
	requestedDirectory := ""
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"sudo": {Inline: "echo sudo", Dir: "plugins/sudo"},
	}}

	if _, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{
		directory: t.TempDir(),
		lastDir:   &requestedDirectory,
	}, ModeNormal); err != nil {
		t.Fatal(err)
	}
	if requestedDirectory != "plugins/sudo" {
		t.Fatalf("requested directory = %q", requestedDirectory)
	}
}

func TestBuildRecordsGitPluginSourceAndRevision(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"zsh-defer": {GitHub: "romkatv/zsh-defer"},
	}}

	locked, err := Build(Context{Shell: "zsh"}, cfg, revisionInstaller{directory: t.TempDir(), revision: "53a26e287fbbe2dcebb3aa1801546c6de32416fa"}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 {
		t.Fatalf("locked plugins = %+v", locked.Plugins)
	}
	plugin := locked.Plugins[0]
	if plugin.Source != "github:romkatv/zsh-defer" {
		t.Fatalf("source = %q", plugin.Source)
	}
	if plugin.Rev != "53a26e287fbbe2dcebb3aa1801546c6de32416fa" {
		t.Fatalf("rev = %q", plugin.Rev)
	}
}

func TestBuildSelectsConfiguredPluginFile(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "sudo.plugin.zsh")
	if err := os.WriteFile(file, []byte("echo sudo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"sudo": {Inline: "echo sudo", File: "sudo.plugin.zsh"},
	}}

	locked, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{directory: directory}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 || len(locked.Plugins[0].Files) != 1 || locked.Plugins[0].Files[0] != file {
		t.Fatalf("selected files = %+v", locked.Plugins)
	}
}

type countingInstaller struct {
	calls int
}

func (installer *countingInstaller) Install(context.Context, source.Request) (source.Installed, error) {
	installer.calls++
	return source.Installed{}, nil
}

type revisionInstaller struct {
	directory string
	revision  string
}

type blockingInstaller struct {
	directory string
	started   chan struct{}
	release   chan struct{}
}

func (installer blockingInstaller) Install(ctx context.Context, request source.Request) (source.Installed, error) {
	installer.started <- struct{}{}
	select {
	case <-installer.release:
	case <-ctx.Done():
		return source.Installed{}, ctx.Err()
	}
	return source.Installed{Directory: installer.directory, File: filepath.Join(installer.directory, request.Name+".plugin.zsh")}, nil
}

func (installer revisionInstaller) Install(_ context.Context, request source.Request) (source.Installed, error) {
	return source.Installed{
		Directory: installer.directory,
		File:      filepath.Join(installer.directory, request.Name+".plugin.zsh"),
		Revision:  installer.revision,
	}, nil
}

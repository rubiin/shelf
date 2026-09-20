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

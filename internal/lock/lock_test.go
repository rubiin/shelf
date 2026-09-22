package lock

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"shelf/internal/config"
	"shelf/internal/source"
)

type testInstaller struct {
	directory   string
	lastDir     *string
	lastRequest *source.Request
}

func (installer testInstaller) Install(_ context.Context, request source.Request) (source.Installed, error) {
	if installer.lastDir != nil {
		*installer.lastDir = request.Dir
	}
	if installer.lastRequest != nil {
		*installer.lastRequest = request
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
	ctx := Context{ConfigFile: filepath.Join(directory, "missing.toml"), ConfigFingerprint: "fingerprint", Shell: "zsh", Templates: map[string]string{"source": "source \"{{ file }}\""}}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{"test": {GitHub: "rubiin/test"}}}
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
	stale := Context{ConfigFile: ctx.ConfigFile, Shell: "zsh", Templates: ctx.Templates}
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

func TestBuildRecordsEnvironmentAssignments(t *testing.T) {
	cfg := config.Config{
		Env:     map[string]string{"ZSH_THEME": "robbyrussell"},
		Plugins: map[string]config.RawPlugin{"demo": {Inline: "echo demo"}},
	}
	locked, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{directory: t.TempDir()}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if locked.Env["ZSH_THEME"] != "robbyrussell" {
		t.Fatalf("locked environment = %v", locked.Env)
	}
}

func TestBuildOmitsMissingOptionalLocalPlugin(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"optional": {Local: filepath.Join(t.TempDir(), "missing"), Optional: true},
	}}
	locked, err := Build(Context{Shell: "zsh"}, cfg, source.NewInstaller(t.TempDir()), ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 0 {
		t.Fatalf("locked plugins = %+v, want none", locked.Plugins)
	}
}

func TestRestoreInstallsRevisionsInParallel(t *testing.T) {
	locked := LockedConfig{Plugins: []LockedPlugin{
		{Name: "first", URL: "https://github.com/rubiin/first", Rev: "aaaaaaaa"},
		{Name: "second", URL: "https://github.com/rubiin/second", Rev: "bbbbbbbb"},
	}}
	installer := blockingInstaller{directory: t.TempDir(), started: make(chan struct{}, 2), release: make(chan struct{})}
	finished := make(chan error, 1)
	go func() { finished <- Restore(locked, installer, DefaultConcurrency) }()
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
	// A plugin without a pinned rev, and one whose source is not a git clone, are both skipped.
	locked := LockedConfig{Plugins: []LockedPlugin{{Name: "local"}, {Name: "gone", Rev: "aaaaaaaa"}}}
	installer := &countingInstaller{}
	if err := Restore(locked, installer, DefaultConcurrency); err != nil {
		t.Fatal(err)
	}
	if installer.calls != 0 {
		t.Fatalf("installer called %d times, want 0", installer.calls)
	}
}

func TestBuildPreservesPluginDeclarationOrder(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"zsh-vi-mode": {GitHub: "rubiin/vi"},
		"zsh-defer":   {GitHub: "rubiin/defer"},
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
			"first":  {GitHub: "rubiin/first"},
			"second": {GitHub: "rubiin/second"},
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
		"sudo": {GitHub: "rubiin/sudo", Dir: "plugins/sudo"},
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

func TestBuildRecordsMultiForgePluginSources(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"glab": {GitLab: "owner/glab"},
		"bb":   {Bitbucket: "team/bb"},
		"cb":   {Codeberg: "owner/cb"},
	}}
	locked, err := Build(Context{Shell: "zsh"}, cfg, revisionInstaller{directory: t.TempDir(), revision: "abc123"}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"glab": "gitlab:owner/glab",
		"bb":   "bitbucket:team/bb",
		"cb":   "codeberg:owner/cb",
	}
	for _, plugin := range locked.Plugins {
		if plugin.Source != want[plugin.Name] {
			t.Fatalf("plugin %q source = %q, want %q", plugin.Name, plugin.Source, want[plugin.Name])
		}
	}
}

func TestBuildPassesCloneOptionsAndDepthToInstaller(t *testing.T) {
	depth := 2
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {GitHub: "romkatv/zsh-defer", CloneOpts: []string{"--single-branch"}, Depth: &depth},
	}}
	var request source.Request
	locked, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{directory: t.TempDir(), lastRequest: &request}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 {
		t.Fatalf("locked plugins = %+v", locked.Plugins)
	}
	if len(request.CloneOpts) != 1 || request.CloneOpts[0] != "--single-branch" {
		t.Fatalf("installer cloneopts = %v", request.CloneOpts)
	}
	if request.Depth == nil || *request.Depth != 2 {
		t.Fatalf("installer depth = %v, want 2", request.Depth)
	}
	// The lock records them so Restore reinstalls with the same clone behavior.
	plugin := locked.Plugins[0]
	if len(plugin.CloneOpts) != 1 || plugin.CloneOpts[0] != "--single-branch" {
		t.Fatalf("locked cloneopts = %v", plugin.CloneOpts)
	}
	if plugin.Depth == nil || *plugin.Depth != 2 {
		t.Fatalf("locked depth = %v, want 2", plugin.Depth)
	}
	// A depth of 0 must survive a lock write/read round trip.
	zero := 0
	locked.Plugins[0].Depth = &zero
	path := filepath.Join(t.TempDir(), "plugins.lock")
	if err := Write(path, locked); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Plugins[0].Depth == nil || *reloaded.Plugins[0].Depth != 0 {
		t.Fatalf("round-tripped depth = %v, want 0", reloaded.Plugins[0].Depth)
	}
}

// remoteETagInstaller installs a single file carrying the response validator a remote source would record.
type remoteETagInstaller struct {
	directory string
	etag      string
}

func (installer remoteETagInstaller) Install(_ context.Context, request source.Request) (source.Installed, error) {
	return source.Installed{Directory: installer.directory, File: filepath.Join(installer.directory, request.Name+".plugin.zsh"), ETag: installer.etag}, nil
}

func TestBuildPassesStoredRemoteETagToInstaller(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"remote": {Remote: "https://example.com/plugin.zsh"},
	}}
	var request source.Request
	ctx := Context{Shell: "zsh", PreviousETags: map[string]string{"remote": `"573a1e10"`}}
	if _, err := Build(ctx, cfg, testInstaller{directory: t.TempDir(), lastRequest: &request}, ModeUpdate); err != nil {
		t.Fatal(err)
	}
	if request.ETag != `"573a1e10"` {
		t.Fatalf("installer etag = %q, want the previous lock's validator", request.ETag)
	}
}

func TestBuildRecordsRemoteETagInLock(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "remote.plugin.zsh")
	if err := os.WriteFile(file, []byte("echo remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"remote": {Remote: "https://example.com/plugin.zsh"},
	}}
	locked, err := Build(Context{Shell: "zsh"}, cfg, remoteETagInstaller{directory: directory, etag: `"573a1e10"`}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 {
		t.Fatalf("locked plugins = %+v", locked.Plugins)
	}
	if locked.Plugins[0].ETag != `"573a1e10"` {
		t.Fatalf("locked etag = %q, want the validator the installer recorded", locked.Plugins[0].ETag)
	}
}

func TestBuildSkipsUpdateFetchForFrozenPlugin(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {GitHub: "romkatv/zsh-defer", Frozen: true},
	}}
	var request source.Request
	if _, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{directory: t.TempDir(), lastRequest: &request}, ModeUpdate); err != nil {
		t.Fatal(err)
	}
	if request.Update {
		t.Error("frozen plugin update = true, want the fetch skipped")
	}
	if !request.Frozen {
		t.Error("frozen flag did not reach the installer")
	}
	if request.Reinstall {
		t.Error("frozen plugin reinstall unexpectedly set")
	}
}

func TestBuildForceRefreshesFrozenPluginOnUpdate(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {GitHub: "romkatv/zsh-defer", Frozen: true},
	}}
	var request source.Request
	ctx := Context{Shell: "zsh", Force: true}
	if _, err := Build(ctx, cfg, testInstaller{directory: t.TempDir(), lastRequest: &request}, ModeUpdate); err != nil {
		t.Fatal(err)
	}
	if !request.Update {
		t.Error("forced update of a frozen plugin was skipped")
	}
	if request.Reinstall {
		t.Error("forced update reinstalled instead of updating")
	}
}

func TestBuildReinstallRefreshesFrozenPlugin(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {GitHub: "romkatv/zsh-defer", Frozen: true},
	}}
	var request source.Request
	if _, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{directory: t.TempDir(), lastRequest: &request}, ModeReinstall); err != nil {
		t.Fatal(err)
	}
	if !request.Reinstall {
		t.Error("frozen plugin was not reinstalled")
	}
	if request.Update {
		t.Error("reinstall set the update flag")
	}
}

func TestBuildRecordsFrozenPlugins(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {GitHub: "romkatv/zsh-defer", Frozen: true},
	}}
	locked, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{directory: t.TempDir()}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 || !locked.Plugins[0].Frozen {
		t.Fatalf("locked plugins = %+v, want the frozen flag recorded", locked.Plugins)
	}
}

func TestPluginETagsExtractsRemoteValidators(t *testing.T) {
	locked := LockedConfig{Plugins: []LockedPlugin{
		{Name: "remote", ETag: `"v1"`},
		{Name: "plain"},
		{Name: "other", ETag: `"v2"`},
	}}
	etags := PluginETags(locked)
	if len(etags) != 2 || etags["remote"] != `"v1"` || etags["other"] != `"v2"` || etags["plain"] != "" {
		t.Fatalf("plugin etags = %v", etags)
	}
}

func TestRevisionManifestPersistsGitRevisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.lock")
	manifest := RevisionManifest{Plugins: []RevisionPlugin{{
		Name:   "zsh-defer",
		Source: "github:romkatv/zsh-defer",
		Rev:    "53a26e287fbbe2dcebb3aa1801546c6de32416fa",
	}}}
	if err := WriteRevisionManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadRevisionManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(loaded.Plugins, manifest.Plugins, func(left, right RevisionPlugin) bool { return left == right }) {
		t.Fatalf("manifest = %+v, want %+v", loaded, manifest)
	}
}

func TestApplyRevisionManifestPinsMatchingGitSources(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"defer": {GitHub: "romkatv/zsh-defer", Branch: "main"},
		"other": {GitHub: "example/other"},
	}}
	manifest := RevisionManifest{Plugins: []RevisionPlugin{
		{Name: "defer", Source: "github:romkatv/zsh-defer", Rev: "53a26e287fbbe2dcebb3aa1801546c6de32416fa"},
		{Name: "other", Source: "github:example/different", Rev: "ignored"},
	}}
	pinned := ApplyRevisionManifest(cfg, manifest)
	if pinned.Plugins["defer"].Rev != "53a26e287fbbe2dcebb3aa1801546c6de32416fa" {
		t.Fatalf("pinned revision = %q", pinned.Plugins["defer"].Rev)
	}
	if pinned.Plugins["other"].Rev != "" {
		t.Fatalf("mismatched source revision = %q", pinned.Plugins["other"].Rev)
	}
}

func TestRevisionManifestFromLockedConfigExcludesNonGitPlugins(t *testing.T) {
	locked := LockedConfig{Plugins: []LockedPlugin{
		{Name: "git", Source: "github:example/plugin", Rev: "abc"},
		{Name: "local", Rev: "def"},
		{Name: "remote", Source: "remote:https://example.test/plugin", Rev: "ghi"},
	}}
	manifest := RevisionManifestFrom(locked)
	if len(manifest.Plugins) != 1 || manifest.Plugins[0].Name != "git" {
		t.Fatalf("manifest plugins = %+v", manifest.Plugins)
	}
}

func TestBuildSelectsConfiguredPluginFile(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "sudo.plugin.zsh")
	if err := os.WriteFile(file, []byte("echo sudo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"sudo": {GitHub: "rubiin/sudo", File: "sudo.plugin.zsh"},
	}}

	locked, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{directory: directory}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 || len(locked.Plugins[0].Files) != 1 || locked.Plugins[0].Files[0] != file {
		t.Fatalf("selected files = %+v", locked.Plugins)
	}
}

// directoryOnlyInstaller installs a directory without pinning a single file, leaving selection to `use`.
type directoryOnlyInstaller struct{ directory string }

func (installer directoryOnlyInstaller) Install(context.Context, source.Request) (source.Installed, error) {
	return source.Installed{Directory: installer.directory}, nil
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

func TestBuildRecordsInlinePluginsAndTemplates(t *testing.T) {
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"test": {Inline: "echo {{ name }}", Hooks: map[string]string{"pre": "echo pre"}},
	}}
	installer := &countingInstaller{}
	templates := map[string]string{"source": "source \"{{ file }}\""}
	locked, err := Build(Context{Shell: "zsh", Templates: templates}, cfg, installer, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if installer.calls != 0 {
		t.Fatalf("installer called %d times, want 0 for an inline plugin", installer.calls)
	}
	if locked.Templates["source"] != templates["source"] {
		t.Fatalf("locked templates = %v", locked.Templates)
	}
	plugin := locked.Plugins[0]
	if plugin.Inline != "echo {{ name }}" || len(plugin.Files) != 0 || plugin.Directory != "" {
		t.Fatalf("locked plugin = %+v", plugin)
	}
}

func TestVerifyRejectsLockWithoutTemplates(t *testing.T) {
	// A lock written before templates were recorded is rebuilt once.
	directory := t.TempDir()
	path := filepath.Join(directory, "plugins.lock")
	locked := LockedConfig{ConfigFingerprint: "fingerprint", Shell: "zsh"}
	if err := Write(path, locked); err != nil {
		t.Fatal(err)
	}
	ctx := Context{ConfigFingerprint: "fingerprint", Shell: "zsh"}
	if valid, err := Verify(path, ctx); err != nil || valid {
		t.Fatalf("verify = %v, err = %v", valid, err)
	}
}

func TestBuildUnionsEveryUsePattern(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"demo.plugin.zsh", "demo.extra.sh", "other.zsh"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("echo "+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {GitHub: "rubiin/demo", Use: []string{"demo.*.zsh", "demo.*.sh"}},
	}}

	locked, err := Build(Context{Shell: "zsh"}, cfg, directoryOnlyInstaller{directory: directory}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	// Both patterns are walked together, so the selection is ordered by file name.
	want := []string{filepath.Join(directory, "demo.extra.sh"), filepath.Join(directory, "demo.plugin.zsh")}
	if len(locked.Plugins) != 1 || !slices.Equal(locked.Plugins[0].Files, want) {
		t.Fatalf("selected files = %v, want %v", locked.Plugins[0].Files, want)
	}
}

type buildInstaller struct {
	directory string
	root      string
}

func (installer buildInstaller) Install(_ context.Context, _ source.Request) (source.Installed, error) {
	return source.Installed{Directory: installer.directory, Root: installer.root}, nil
}

func TestBuildRunsBeforeFileSelection(t *testing.T) {
	directory := t.TempDir()
	ctx := Context{Shell: "zsh", Templates: map[string]string{"source": "source \"{{ file }}\""}, Diagnostics: io.Discard}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {Local: directory, Build: []string{"touch generated.zsh"}, Use: []string{"generated.zsh"}},
	}}
	locked, err := Build(ctx, cfg, buildInstaller{directory: directory, root: directory}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 1 || len(locked.Plugins[0].Files) != 1 || filepath.Base(locked.Plugins[0].Files[0]) != "generated.zsh" {
		t.Fatalf("locked plugins = %+v, want the generated file selected", locked.Plugins)
	}
}

func TestBuildRunsInTheSourceRootNotTheSubdirectory(t *testing.T) {
	root := t.TempDir()
	pluginDirectory := filepath.Join(root, "plugins", "demo")
	if err := os.MkdirAll(pluginDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := Context{Shell: "zsh", Templates: map[string]string{"source": "source \"{{ file }}\""}, Diagnostics: nil}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {Local: root, Dir: "plugins/demo", Build: []string{"printf '%s' \"$PWD\" > built-from.txt"}},
	}}
	locked, err := Build(ctx, cfg, buildInstaller{directory: pluginDirectory, root: root}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	contents, readErr := os.ReadFile(filepath.Join(root, "built-from.txt"))
	if readErr != nil {
		t.Fatalf("build did not write in the source root: %v", readErr)
	}
	if string(contents) != root {
		t.Fatalf("build ran in %q, want the source root %q", contents, root)
	}
	if len(locked.Plugins) != 1 {
		t.Fatalf("locked plugins = %+v", locked.Plugins)
	}
}

func TestBuildFailureFailsTheLock(t *testing.T) {
	directory := t.TempDir()
	ctx := Context{Shell: "zsh"}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {Local: directory, Build: []string{"exit 1"}},
	}}
	_, err := Build(ctx, cfg, buildInstaller{directory: directory, root: directory}, ModeNormal)
	if err == nil || !strings.Contains(err.Error(), `build plugin "demo"`) {
		t.Fatalf("error = %v, want a build failure naming the plugin", err)
	}
}

func TestBuildOutputStreamsToDiagnostics(t *testing.T) {
	directory := t.TempDir()
	var diagnostics bytes.Buffer
	ctx := Context{Shell: "zsh", Diagnostics: &diagnostics}
	cfg := config.Config{Plugins: map[string]config.RawPlugin{
		"demo": {Local: directory, Build: []string{"echo built-here"}},
	}}
	if _, err := Build(ctx, cfg, buildInstaller{directory: directory, root: directory}, ModeNormal); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), "built-here") {
		t.Fatalf("diagnostics = %q, want the build output", diagnostics.String())
	}
}

// TestSelectedFilesExistCoversBothPaths exercises the inline (sequential) and the
// parallel verification paths, which is what selectedFilesExist'ing a large shell hits.
func TestSelectedFilesExistCoversBothPaths(t *testing.T) {
	pluginFiles := func(count int, missingAt int) LockedConfig {
		files := make([]string, count)
		for index := range count {
			path := filepath.Join(t.TempDir(), fmt.Sprintf("%d.plugin.zsh", index))
			if index != missingAt {
				if err := os.WriteFile(path, []byte("echo hi\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			files[index] = path
		}
		return LockedConfig{Plugins: []LockedPlugin{{Name: "demo", Files: files}}}
	}

	tests := []struct {
		name      string
		locked    LockedConfig
		wantValid bool
	}{
		{name: "few files, all present", locked: pluginFiles(3, -1), wantValid: true},
		{name: "few files, one missing", locked: pluginFiles(3, 1), wantValid: false},
		{name: "many files, all present", locked: pluginFiles(verifyParallelAt+1, -1), wantValid: true},
		{name: "many files, one missing", locked: pluginFiles(verifyParallelAt+1, verifyParallelAt), wantValid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := selectedFilesExist(test.locked); got != test.wantValid {
				t.Fatalf("selectedFilesExist = %v, want %v", got, test.wantValid)
			}
		})
	}
}

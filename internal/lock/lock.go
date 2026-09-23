package lock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/BurntSushi/toml"
	"shelf/internal/config"
	"shelf/internal/source"
)

const DefaultConcurrency = 8

const (
	// Above this many selected files, verification runs in parallel.
	verifyParallelAt  = 16
	verifyConcurrency = 8
)

var errFileMissing = errors.New("selected plugin file is missing")

// Uses ConfigFingerprint when set, else hashes ConfigFile.
func (ctx Context) fingerprint() string {
	if ctx.ConfigFingerprint != "" {
		return ctx.ConfigFingerprint
	}
	return fingerprint(ctx.ConfigFile)
}

func Build(ctx Context, cfg config.Config, installer source.Installer, mode Mode) (LockedConfig, error) {
	return BuildWithConcurrency(ctx, cfg, installer, mode, DefaultConcurrency)
}

func BuildWithConcurrency(ctx Context, cfg config.Config, installer source.Installer, mode Mode, concurrency int) (LockedConfig, error) {
	locked := LockedConfig{ConfigFingerprint: ctx.fingerprint(), Profile: ctx.Profile, Shell: ctx.Shell, Env: cfg.Env, Templates: ctx.Templates}
	if ctx.Profile != "" && !ProfileMatches(cfg, ctx.Profile) {
		locked.ProfileMatch = "unmatched"
	}
	type task struct {
		name   string
		plugin config.RawPlugin
	}
	var tasks []task
	for _, name := range PluginNames(cfg) {
		plugin := cfg.Plugins[name]
		if !Active(plugin.Profiles, ctx.Profile) {
			continue
		}
		tasks = append(tasks, task{name: name, plugin: plugin})
	}
	plugins := make([]LockedPlugin, len(tasks))
	err := RunConcurrently(context.Background(), len(tasks), concurrency, func(installContext context.Context, index int) error {
		plugin, err := buildPlugin(installContext, ctx, cfg, installer, mode, tasks[index].name, tasks[index].plugin)
		if err != nil {
			return err
		}
		plugins[index] = plugin
		return nil
	})
	if err != nil {
		return LockedConfig{}, err
	}
	locked.Plugins = plugins[:0]
	for _, plugin := range plugins {
		if plugin.Name != "" {
			locked.Plugins = append(locked.Plugins, plugin)
		}
	}
	return locked, nil
}

// RunConcurrently runs work per index with at most concurrency workers, stopping on the first error.
// The ctx is passed to every work call; its cancellation or deadline bounds the whole run.
func RunConcurrently(ctx context.Context, count, concurrency int, work func(ctx context.Context, index int) error) error {
	if concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if count == 0 {
		return nil
	}
	if count == 1 {
		return work(ctx, 0)
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int, min(concurrency, count))
	var waitGroup sync.WaitGroup
	var once sync.Once
	var firstErr error
	for range min(concurrency, count) {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for {
				select {
				case <-runContext.Done():
					return
				case index, open := <-jobs:
					if !open {
						return
					}
					if err := work(runContext, index); err != nil {
						once.Do(func() {
							firstErr = err
							cancel()
						})
						return
					}
				}
			}
		}()
	}
dispatch:
	for index := range count {
		select {
		case <-runContext.Done():
			break dispatch
		case jobs <- index:
		}
	}
	close(jobs)
	waitGroup.Wait()
	return firstErr
}

func buildPlugin(installContext context.Context, ctx Context, cfg config.Config, installer source.Installer, mode Mode, name string, plugin config.RawPlugin) (LockedPlugin, error) {
	// Inline plugins have no install step; the lock carries their text.
	if plugin.Inline != "" {
		return LockedPlugin{Name: name, Inline: plugin.Inline, Hooks: plugin.Hooks}, nil
	}
	// Frozen plugins keep their pin on update; --force and --reinstall still refresh.
	update := mode == ModeUpdate && (!plugin.Frozen || ctx.Force)
	installed, err := installer.Install(installContext, source.Request{
		Name: name, Git: plugin.Git, GitHub: plugin.GitHub, Gist: plugin.Gist, GitLab: plugin.GitLab, Bitbucket: plugin.Bitbucket, Codeberg: plugin.Codeberg, Proto: plugin.Proto, Remote: plugin.Remote,
		Local: plugin.Local, Optional: plugin.Optional, Ref: plugin.Rev, Branch: plugin.Branch,
		Tag: plugin.Tag, Dir: plugin.Dir, File: plugin.File, Update: update, Reinstall: mode == ModeReinstall, Frozen: plugin.Frozen,
		ETag: ctx.PreviousETags[name], CloneOpts: plugin.CloneOpts, Depth: plugin.Depth,
	})
	if err != nil {
		return LockedPlugin{}, fmt.Errorf("install plugin %q: %w", name, err)
	}
	if installed.Skipped {
		return LockedPlugin{}, nil
	}
	if err := runBuild(installContext, ctx.Diagnostics, name, installed, plugin.Build); err != nil {
		return LockedPlugin{}, err
	}
	var files []string
	if plugin.File != "" {
		file := filepath.Join(installed.Directory, plugin.File)
		if _, err := os.Stat(file); err != nil {
			return LockedPlugin{}, fmt.Errorf("select plugin %q file %q: %w", name, plugin.File, err)
		}
		files = []string{file}
	} else if installed.File != "" {
		files = []string{installed.File}
	} else {
		// `use` tries every pattern; global matches stop at the first pattern that selects anything.
		patterns := plugin.Use
		firstMatch := false
		if len(patterns) == 0 {
			patterns = cfg.Matches
			if len(patterns) == 0 {
				patterns = defaultMatches(ctx.Shell)
			}
			firstMatch = true
		}
		files, err = selectFiles(installed.Directory, name, ctx.Shell, patterns, firstMatch, plugin.Ignore)
		if err != nil {
			return LockedPlugin{}, fmt.Errorf("select plugin %q files: %w", name, err)
		}
	}
	apply := plugin.Apply
	if len(apply) == 0 {
		apply = cfg.Apply
	}
	if len(apply) == 0 {
		apply = []string{"source"}
	}
	return LockedPlugin{Name: name, Source: pluginSource(plugin), URL: pluginCloneURL(plugin), Rev: installed.Revision, ETag: installed.ETag, Directory: installed.Directory, Files: files, Apply: apply, Hooks: plugin.Hooks, CloneOpts: plugin.CloneOpts, Depth: plugin.Depth, Frozen: plugin.Frozen, Ignore: plugin.Ignore}, nil
}

// PluginETags maps each plugin name to the ETag its lock entry recorded, for a conditional GET on the next update.
func PluginETags(locked LockedConfig) map[string]string {
	etags := make(map[string]string)
	for _, plugin := range locked.Plugins {
		if plugin.ETag != "" {
			etags[plugin.Name] = plugin.ETag
		}
	}
	return etags
}

// runBuild runs each command with sh -c in the plugin's source root.
func runBuild(ctx context.Context, diagnostics io.Writer, name string, installed source.Installed, commands []string) error {
	if len(commands) == 0 {
		return nil
	}
	if diagnostics == nil {
		diagnostics = io.Discard
	}
	directory := installed.Root
	if directory == "" {
		directory = installed.Directory
	}
	for _, command := range commands {
		process := exec.CommandContext(ctx, "sh", "-c", command)
		process.Dir = directory
		process.Stdout = diagnostics
		process.Stderr = diagnostics
		if err := process.Run(); err != nil {
			return fmt.Errorf("build plugin %q in %q: command %q: %w", name, directory, command, err)
		}
	}
	return nil
}

// Restore reinstalls the pinned revisions using only the lock.
func Restore(locked LockedConfig, installer source.Installer, concurrency int) error {
	var tasks []LockedPlugin
	for _, plugin := range locked.Plugins {
		// Only git sources record a URL; older locks without one are skipped.
		if plugin.Rev == "" || plugin.URL == "" {
			continue
		}
		tasks = append(tasks, plugin)
	}
	return RunConcurrently(context.Background(), len(tasks), concurrency, func(installContext context.Context, index int) error {
		plugin := tasks[index]
		if _, err := installer.Install(installContext, source.Request{Name: plugin.Name, Git: plugin.URL, Ref: plugin.Rev, CloneOpts: plugin.CloneOpts, Depth: plugin.Depth}); err != nil {
			return fmt.Errorf("restore plugin %q revision %q: %w", plugin.Name, plugin.Rev, err)
		}
		return nil
	})
}

func pluginSource(plugin config.RawPlugin) string {
	switch {
	case plugin.GitHub != "":
		return "github:" + plugin.GitHub
	case plugin.Gist != "":
		return "gist:" + plugin.Gist
	case plugin.GitLab != "":
		return "gitlab:" + plugin.GitLab
	case plugin.Bitbucket != "":
		return "bitbucket:" + plugin.Bitbucket
	case plugin.Codeberg != "":
		return "codeberg:" + plugin.Codeberg
	case plugin.Git != "":
		return "git:" + plugin.Git
	default:
		return ""
	}
}

func isGit(plugin config.RawPlugin) bool {
	return plugin.Git != "" || plugin.GitHub != "" || plugin.Gist != "" || plugin.GitLab != "" || plugin.Bitbucket != "" || plugin.Codeberg != ""
}

// pluginCloneURL resolves the clone URL recorded in the lock, so Restore needs no config.
func pluginCloneURL(plugin config.RawPlugin) string {
	if !isGit(plugin) {
		return ""
	}
	return source.CloneURL(source.Request{Git: plugin.Git, GitHub: plugin.GitHub, Gist: plugin.Gist, GitLab: plugin.GitLab, Bitbucket: plugin.Bitbucket, Codeberg: plugin.Codeberg, Proto: plugin.Proto})
}

// PluginNames returns plugin names in declaration order, then remaining names sorted.
func PluginNames(cfg config.Config) []string {
	seen := make(map[string]bool, len(cfg.Plugins))
	names := make([]string, 0, len(cfg.Plugins))
	for _, name := range cfg.PluginOrder {
		if _, exists := cfg.Plugins[name]; exists && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	remaining := make([]string, 0, len(cfg.Plugins)-len(names))
	for name := range cfg.Plugins {
		if !seen[name] {
			remaining = append(remaining, name)
		}
	}
	sort.Strings(remaining)
	return append(names, remaining...)
}

// Active reports whether a plugin loads for the profile; a plugin with profiles needs one selected.
func Active(profiles []string, profile string) bool {
	if len(profiles) == 0 {
		return true
	}
	if profile == "" {
		return false
	}
	for _, candidate := range profiles {
		if candidate == profile {
			return true
		}
	}
	return false
}

func ProfileMatches(cfg config.Config, profile string) bool {
	if profile == "" {
		return true
	}
	for _, plugin := range cfg.Plugins {
		for _, candidate := range plugin.Profiles {
			if candidate == profile {
				return true
			}
		}
	}
	return false
}

func ApplyRevisionManifest(cfg config.Config, manifest RevisionManifest) config.Config {
	entries := make(map[string]RevisionPlugin, len(manifest.Plugins))
	for _, plugin := range manifest.Plugins {
		entries[plugin.Name] = plugin
	}
	pinned := cfg
	pinned.Plugins = make(map[string]config.RawPlugin, len(cfg.Plugins))
	for name, plugin := range cfg.Plugins {
		if entry, exists := entries[name]; exists && entry.Source == pluginSource(plugin) && entry.Rev != "" {
			plugin.Rev = entry.Rev
		}
		pinned.Plugins[name] = plugin
	}
	return pinned
}

func RevisionManifestFrom(locked LockedConfig) RevisionManifest {
	manifest := RevisionManifest{}
	for _, plugin := range locked.Plugins {
		if isRevisionSource(plugin.Source) && plugin.Rev != "" {
			manifest.Plugins = append(manifest.Plugins, RevisionPlugin{Name: plugin.Name, Source: plugin.Source, Rev: plugin.Rev})
		}
	}
	return manifest
}

func isRevisionSource(value string) bool {
	for _, prefix := range []string{"github:", "gist:", "gitlab:", "bitbucket:", "codeberg:", "git:"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// Write encodes to a temp file and renames it atomically, syncing both the file
// and the directory so a crash can't truncate the lock or lose the rename.
func Write(path string, locked LockedConfig) error {
	return writeTOML(path, locked)
}

func WriteRevisionManifest(path string, manifest RevisionManifest) error {
	return writeTOML(path, manifest)
}

func writeTOML(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".plugins-*.lock")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := toml.NewEncoder(temporary).Encode(value); err != nil {
		_ = temporary.Close()
		return err
	}
	// Flush the temp contents to disk before the rename, so a crash right after
	// the rename can't leave a zero-length or partial lock in place.
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync lock file %q: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	// Sync the containing directory so the rename itself is durable, not just
	// the replacement file.
	handle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open lock directory %q: %w", directory, err)
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return fmt.Errorf("sync lock directory %q: %w", directory, err)
	}
	return handle.Close()
}

// Read decodes a lock file, using the schema-specific reader when it matches.
func Read(path string) (LockedConfig, error) {
	return readLockFile(path)
}

func ReadRevisionManifest(path string) (RevisionManifest, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return RevisionManifest{}, err
	}
	var manifest RevisionManifest
	if err := toml.Unmarshal(contents, &manifest); err != nil {
		return RevisionManifest{}, err
	}
	return manifest, nil
}

func Verify(path string, ctx Context) (bool, error) {
	locked, err := Read(path)
	if err != nil {
		return false, err
	}
	return VerifyLocked(locked, ctx), nil
}

// VerifyLocked checks an already-read lock against the context.
func VerifyLocked(locked LockedConfig, ctx Context) bool {
	// A lock without templates predates them and must be rebuilt once.
	if len(locked.Templates) == 0 {
		return false
	}
	// Rendering replays the lock's templates, so a shelf upgrade that changes
	// them must invalidate the lock even when the config fingerprint matches.
	// Only contexts that resolved current templates gate on them; callers that
	// don't render (status, doctor) skip the comparison.
	if ctx.Templates != nil && !maps.Equal(locked.Templates, ctx.Templates) {
		return false
	}
	if locked.Profile != ctx.Profile || locked.Shell != ctx.Shell || locked.ConfigFingerprint != ctx.fingerprint() {
		return false
	}
	return selectedFilesExist(locked)
}

func selectedFilesExist(locked LockedConfig) bool {
	total := 0
	for _, plugin := range locked.Plugins {
		total += len(plugin.Files)
	}
	if total == 0 {
		return true
	}
	// Few stats are quicker inline; many are quicker in parallel.
	if total < verifyParallelAt {
		for _, plugin := range locked.Plugins {
			for _, file := range plugin.Files {
				if !exists(file) {
					return false
				}
			}
		}
		return true
	}
	files := make([]string, 0, total)
	for _, plugin := range locked.Plugins {
		files = append(files, plugin.Files...)
	}
	missing := RunConcurrently(context.Background(), total, verifyConcurrency, func(_ context.Context, index int) error {
		if exists(files[index]) {
			return nil
		}
		return errFileMissing
	})
	return missing == nil
}

// exists stats the path without allocating a FileInfo.
func exists(path string) bool {
	var status syscall.Stat_t
	return syscall.Stat(path, &status) == nil
}

// Fingerprint is the hash lock files record as config_fingerprint.
func Fingerprint(contents []byte) string {
	hash := sha256.Sum256(contents)
	return hex.EncodeToString(hash[:])
}

func fingerprint(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return Fingerprint(contents)
}

package lock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	// verifyParallelAt is the selected-file count above which verification runs wide.
	verifyParallelAt = 16
	// verifyConcurrency is the worker count used to check selected plugin files.
	verifyConcurrency = 8
)

var errFileMissing = errors.New("selected plugin file is missing")

// fingerprint returns the supplied config fingerprint, or computes it from ConfigFile.
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
	err := RunConcurrently(len(tasks), concurrency, func(installContext context.Context, index int) error {
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

// RunConcurrently runs work per index with at most concurrency workers, cancelling the rest on
// failure. Callers that need every result (such as a status check that reports all plugins) can
// simply never return an error, keeping all workers running to completion.
func RunConcurrently(count, concurrency int, work func(ctx context.Context, index int) error) error {
	if concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if count == 0 {
		return nil
	}
	if count == 1 {
		return work(context.Background(), 0)
	}
	installContext, cancel := context.WithCancel(context.Background())
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
				case <-installContext.Done():
					return
				case index, open := <-jobs:
					if !open {
						return
					}
					if err := work(installContext, index); err != nil {
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
		case <-installContext.Done():
			break dispatch
		case jobs <- index:
		}
	}
	close(jobs)
	waitGroup.Wait()
	return firstErr
}

func buildPlugin(installContext context.Context, ctx Context, cfg config.Config, installer source.Installer, mode Mode, name string, plugin config.RawPlugin) (LockedPlugin, error) {
	// Inline plugins have nothing to install: the lock carries their text and renders it.
	if plugin.Inline != "" {
		return LockedPlugin{Name: name, Inline: plugin.Inline, Hooks: plugin.Hooks}, nil
	}
	installed, err := installer.Install(installContext, source.Request{
		Name: name, Git: plugin.Git, GitHub: plugin.GitHub, Gist: plugin.Gist, GitLab: plugin.GitLab, Bitbucket: plugin.Bitbucket, Codeberg: plugin.Codeberg, Proto: plugin.Proto, Remote: plugin.Remote,
		Local: plugin.Local, Optional: plugin.Optional, Ref: plugin.Rev, Branch: plugin.Branch,
		Tag: plugin.Tag, Dir: plugin.Dir, File: plugin.File, Update: mode == ModeUpdate, Reinstall: mode == ModeReinstall,
		CloneOpts: plugin.CloneOpts, Depth: plugin.Depth,
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
		// `use` lists every pattern to select from, while global matches stop at the first pattern
		// that selects anything.
		patterns := plugin.Use
		firstMatch := false
		if len(patterns) == 0 {
			patterns = cfg.Matches
			if len(patterns) == 0 {
				patterns = defaultMatches(ctx.Shell)
			}
			firstMatch = true
		}
		files, err = selectFiles(installed.Directory, name, ctx.Shell, patterns, firstMatch)
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
	return LockedPlugin{Name: name, Source: pluginSource(plugin), URL: pluginCloneURL(plugin), Rev: installed.Revision, Directory: installed.Directory, Files: files, Apply: apply, Hooks: plugin.Hooks, CloneOpts: plugin.CloneOpts, Depth: plugin.Depth}, nil
}

// runBuild executes each build command with the POSIX shell in the plugin's source
// root (falling back to its selected directory). The command text is user content;
// shelf builds only the argv, never a concatenated shell string.
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

// Restore reinstalls the revisions the lock pinned, using only the lock so a caller that holds a
// valid lock never parses the config. It runs through the same worker pool as locking.
func Restore(locked LockedConfig, installer source.Installer, concurrency int) error {
	var tasks []LockedPlugin
	for _, plugin := range locked.Plugins {
		// Only git sources record a URL; a lock written before URLs were recorded is left as it is.
		if plugin.Rev == "" || plugin.URL == "" {
			continue
		}
		tasks = append(tasks, plugin)
	}
	return RunConcurrently(len(tasks), concurrency, func(installContext context.Context, index int) error {
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

// pluginCloneURL resolves a git source's clone URL, which the lock records so Restore needs no config.
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

// Write encodes through a temp file and an atomic rename, so a crash cannot truncate the lock.
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
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

// Read decodes a lock file, using the schema-specific reader when the contents match it.
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

// VerifyLocked checks an already-read lock file against the context, avoiding a second read.
func VerifyLocked(locked LockedConfig, ctx Context) bool {
	// A lock without templates predates lock-recorded templates, so it is rebuilt once.
	if len(locked.Templates) == 0 {
		return false
	}
	if locked.Profile != ctx.Profile || locked.Shell != ctx.Shell || locked.ConfigFingerprint != ctx.fingerprint() {
		return false
	}
	return selectedFilesExist(locked)
}

// selectedFilesExist reports whether every selected plugin file is still installed.
func selectedFilesExist(locked LockedConfig) bool {
	total := 0
	for _, plugin := range locked.Plugins {
		total += len(plugin.Files)
	}
	if total == 0 {
		return true
	}
	files := make([]string, 0, total)
	for _, plugin := range locked.Plugins {
		files = append(files, plugin.Files...)
	}
	// A few stats are quicker inline; a shell with many plugins is quicker in parallel.
	if total < verifyParallelAt {
		for _, file := range files {
			if !exists(file) {
				return false
			}
		}
		return true
	}
	missing := RunConcurrently(total, verifyConcurrency, func(_ context.Context, index int) error {
		if exists(files[index]) {
			return nil
		}
		return errFileMissing
	})
	return missing == nil
}

// exists reports whether a path exists, without allocating a FileInfo for every check.
func exists(path string) bool {
	var status syscall.Stat_t
	return syscall.Stat(path, &status) == nil
}

// Fingerprint returns the config fingerprint recorded in lock files.
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

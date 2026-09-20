package lock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
	"shelf/internal/config"
	"shelf/internal/source"
)

func Build(ctx Context, cfg config.Config, installer source.Installer, mode Mode) (LockedConfig, error) {
	locked := LockedConfig{ConfigFingerprint: fingerprint(ctx.ConfigFile), Profile: ctx.Profile, Shell: ctx.Shell}
	for _, name := range pluginNames(cfg) {
		plugin := cfg.Plugins[name]
		if !active(plugin.Profiles, ctx.Profile) {
			continue
		}
		installed, err := installer.Install(context.Background(), source.Request{
			Name: name, Git: plugin.Git, GitHub: plugin.GitHub, Gist: plugin.Gist, Protocol: plugin.Protocol, Remote: plugin.Remote,
			Local: plugin.Local, Inline: plugin.Inline, Ref: plugin.Rev, Branch: plugin.Branch,
			Tag: plugin.Tag, Dir: plugin.Dir, File: plugin.File, Update: mode == ModeUpdate, Reinstall: mode == ModeReinstall,
		})
		if err != nil {
			return LockedConfig{}, fmt.Errorf("install plugin %q: %w", name, err)
		}
		var files []string
		if plugin.File != "" {
			file := filepath.Join(installed.Directory, plugin.File)
			if _, err := os.Stat(file); err != nil {
				return LockedConfig{}, fmt.Errorf("select plugin %q file %q: %w", name, plugin.File, err)
			}
			files = []string{file}
		} else if installed.File != "" {
			files = []string{installed.File}
		} else {
			patterns := plugin.Use
			firstMatch := len(patterns) > 0
			if len(patterns) == 0 {
				patterns = cfg.Matches
				if len(patterns) == 0 {
					patterns = defaultMatches(ctx.Shell)
				}
				firstMatch = true
			}
			files, err = selectFiles(installed.Directory, name, ctx.Shell, patterns, firstMatch)
			if err != nil {
				return LockedConfig{}, fmt.Errorf("select plugin %q files: %w", name, err)
			}
		}
		apply := plugin.Apply
		if len(apply) == 0 {
			apply = cfg.Apply
		}
		if len(apply) == 0 {
			apply = []string{"source"}
		}
		locked.Plugins = append(locked.Plugins, LockedPlugin{Name: name, Source: pluginSource(plugin), Rev: installed.Revision, Directory: installed.Directory, Files: files, Apply: apply, Hooks: plugin.Hooks})
	}
	return locked, nil
}

func Restore(cfg config.Config, installer source.Installer, locked LockedConfig) error {
	for _, plugin := range locked.Plugins {
		if plugin.Rev == "" {
			continue
		}
		configured, exists := cfg.Plugins[plugin.Name]
		if !exists || !isGit(configured) {
			continue
		}
		if _, err := installer.Install(context.Background(), source.Request{
			Name: plugin.Name, Git: configured.Git, GitHub: configured.GitHub, Gist: configured.Gist, Protocol: configured.Protocol,
			Ref: plugin.Rev, Dir: configured.Dir,
		}); err != nil {
			return fmt.Errorf("restore plugin %q revision %q: %w", plugin.Name, plugin.Rev, err)
		}
	}
	return nil
}

func pluginSource(plugin config.RawPlugin) string {
	switch {
	case plugin.GitHub != "":
		return "github:" + plugin.GitHub
	case plugin.Gist != "":
		return "gist:" + plugin.Gist
	case plugin.Git != "":
		return "git:" + plugin.Git
	default:
		return ""
	}
}

func isGit(plugin config.RawPlugin) bool {
	return plugin.Git != "" || plugin.GitHub != "" || plugin.Gist != ""
}

func pluginNames(cfg config.Config) []string {
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

func active(profiles []string, profile string) bool {
	if len(profiles) == 0 || profile == "" {
		return true
	}
	for _, candidate := range profiles {
		if candidate == profile {
			return true
		}
	}
	return false
}

func Write(path string, locked LockedConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	encodeErr := toml.NewEncoder(file).Encode(locked)
	return errors.Join(encodeErr, file.Close())
}

func Read(path string) (LockedConfig, error) {
	var locked LockedConfig
	if _, err := toml.DecodeFile(path, &locked); err != nil {
		return LockedConfig{}, err
	}
	return locked, nil
}

func Verify(path string, ctx Context) (bool, error) {
	locked, err := Read(path)
	if err != nil {
		return false, err
	}
	if locked.Profile != ctx.Profile || locked.Shell != ctx.Shell || locked.ConfigFingerprint != fingerprint(ctx.ConfigFile) {
		return false, nil
	}
	for _, plugin := range locked.Plugins {
		for _, file := range plugin.Files {
			if _, err := os.Stat(file); err != nil {
				return false, nil
			}
		}
	}
	return true, nil
}

func fingerprint(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(contents)
	return hex.EncodeToString(hash[:])
}

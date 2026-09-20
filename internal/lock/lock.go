package lock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"shelf/internal/config"
	"shelf/internal/source"
)

func Build(ctx Context, cfg config.Config, installer source.Installer, mode Mode) (LockedConfig, error) {
	locked := LockedConfig{ConfigFingerprint: fingerprint(ctx.ConfigFile), Profile: ctx.Profile, Shell: ctx.Shell}
	for name, plugin := range cfg.Plugins {
		if !active(plugin.Profiles, ctx.Profile) {
			continue
		}
		installed, err := installer.Install(context.Background(), source.Request{
			Name: name, Git: plugin.Git, GitHub: plugin.GitHub, Remote: plugin.Remote,
			Local: plugin.Local, Inline: plugin.Inline, Ref: plugin.Rev, Branch: plugin.Branch,
			Tag: plugin.Tag, Update: mode == ModeUpdate, Reinstall: mode == ModeReinstall,
		})
		if err != nil {
			return LockedConfig{}, fmt.Errorf("install plugin %q: %w", name, err)
		}
		files := []string{}
		if installed.File != "" {
			files = []string{installed.File}
		} else {
			files, err = selectFiles(installed.Directory, plugin.Use)
			if err != nil {
				return LockedConfig{}, fmt.Errorf("select plugin %q files: %w", name, err)
			}
		}
		locked.Plugins = append(locked.Plugins, LockedPlugin{Name: name, Directory: installed.Directory, Files: files, Apply: plugin.Apply, Hooks: plugin.Hooks})
	}
	return locked, nil
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
	defer file.Close()
	return toml.NewEncoder(file).Encode(locked)
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

func normalizePath(path string) string {
	return strings.TrimSuffix(filepath.Clean(path), string(filepath.Separator))
}

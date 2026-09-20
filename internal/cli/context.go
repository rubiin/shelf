package cli

import (
	"errors"
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigDirectory string
	DataDirectory   string
	ConfigFile      string
}

// ResolvePaths resolves the config paths: the config file is plugins.toml, and a config
// file given without a directory puts the config directory at its parent.
func ResolvePaths(home, configDirectory, dataDirectory, configFile string) (Paths, error) {
	if home == "" {
		return Paths{}, errors.New("home directory is empty")
	}
	if configDirectory == "" {
		if configFile != "" {
			configDirectory = filepath.Dir(configFile)
		} else {
			configDirectory = filepath.Join(xdgBase("XDG_CONFIG_HOME", home, ".config"), "shelf")
		}
	}
	if configFile == "" {
		configFile = filepath.Join(configDirectory, "plugins.toml")
	}
	if dataDirectory == "" {
		dataDirectory = filepath.Join(xdgBase("XDG_DATA_HOME", home, ".local", "share"), "shelf")
	}
	return Paths{ConfigDirectory: configDirectory, DataDirectory: dataDirectory, ConfigFile: configFile}, nil
}

func xdgBase(name, home string, fallback ...string) string {
	if base := os.Getenv(name); base != "" {
		return base
	}
	return filepath.Join(append([]string{home}, fallback...)...)
}

// LockFile is the lock path for the profile, so profiles keep separate lock files.
func (paths Paths) LockFile(profile string) string {
	if profile == "" {
		return filepath.Join(paths.DataDirectory, "plugins.lock")
	}
	return filepath.Join(paths.DataDirectory, "plugins."+profile+".lock")
}

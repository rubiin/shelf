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

func ResolvePaths(home, configDirectory, dataDirectory, configFile string) (Paths, error) {
	if home == "" {
		return Paths{}, errors.New("home directory is empty")
	}
	if configDirectory == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		configDirectory = filepath.Join(base, "shelf")
	}
	if dataDirectory == "" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			base = filepath.Join(home, ".local", "share")
		}
		dataDirectory = filepath.Join(base, "shelf")
	}
	if configFile == "" {
		configFile = filepath.Join(configDirectory, "config.toml")
	}
	return Paths{ConfigDirectory: configDirectory, DataDirectory: dataDirectory, ConfigFile: configFile}, nil
}

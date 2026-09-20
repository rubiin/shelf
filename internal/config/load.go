package config

import (
	"fmt"
	"net/url"
	"os"

	"github.com/BurntSushi/toml"
)

func Load(path string) (Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Plugins == nil {
		cfg.Plugins = map[string]RawPlugin{}
	}
	return cfg, nil
}

func Validate(cfg Config) error {
	if cfg.Shell != "" && cfg.Shell != Bash && cfg.Shell != Zsh {
		return fmt.Errorf("unsupported shell: %q", cfg.Shell)
	}
	for name, plugin := range cfg.Plugins {
		if name == "" {
			return fmt.Errorf("plugin name is empty")
		}
		sources := 0
		for _, value := range []string{plugin.GitHub, plugin.Git, plugin.Gist, plugin.Remote, plugin.Local, plugin.Inline} {
			if value != "" {
				sources++
			}
		}
		if sources != 1 {
			return fmt.Errorf("plugin %q must have exactly one source", name)
		}
		if plugin.Remote != "" {
			parsed, err := url.Parse(plugin.Remote)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				return fmt.Errorf("plugin %q has invalid remote URL", name)
			}
		}
	}
	return nil
}

func fileExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("config file: %w", err)
	}
	return nil
}

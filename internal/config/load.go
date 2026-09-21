package config

import (
	"fmt"
	"net/url"
	"os"

	"github.com/BurntSushi/toml"
)

// Load reads and decodes the config at path.
func Load(path string) (Config, error) {
	cfg, _, err := LoadWithContents(path)
	return cfg, err
}

// LoadWithContents also returns the bytes it read, so callers need not read the config twice.
func LoadWithContents(path string) (Config, []byte, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, nil, fmt.Errorf("decode config: %w", err)
	}
	cfg, err := decode(contents)
	if err != nil {
		return Config{}, nil, err
	}
	return cfg, contents, nil
}

func decode(contents []byte) (Config, error) {
	var cfg Config
	metadata, err := toml.Decode(string(contents), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Plugins == nil {
		cfg.Plugins = map[string]RawPlugin{}
	}
	for _, key := range metadata.Keys() {
		if len(key) == 2 && key[0] == "plugins" {
			if _, exists := cfg.Plugins[key[1]]; exists {
				cfg.PluginOrder = append(cfg.PluginOrder, key[1])
			}
		}
	}

	for name, plugin := range cfg.Plugins {
		if plugin.Proto == "" && plugin.Protocol != "" {
			plugin.Proto = plugin.Protocol
		}
		plugin.Protocol = ""
		cfg.Plugins[name] = plugin
	}
	return cfg, nil
}

func Validate(cfg Config) error {
	if cfg.Shell != "" && cfg.Shell != Bash && cfg.Shell != Zsh {
		return fmt.Errorf("unsupported shell: %q", cfg.Shell)
	}
	for name := range cfg.Env {
		if !validEnvironmentName(name) {
			return fmt.Errorf("invalid environment variable %q", name)
		}
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
		if plugin.Optional && plugin.Local == "" {
			return fmt.Errorf("plugin %q can only set optional for a local source", name)
		}
		if plugin.Remote != "" {
			parsed, err := url.Parse(plugin.Remote)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				return fmt.Errorf("plugin %q has invalid remote URL", name)
			}
		}
		if plugin.Proto != "" {
			switch plugin.Proto {
			case "git", "https", "ssh":
			default:
				return fmt.Errorf("plugin %q proto %q must be git, https, or ssh", name, plugin.Proto)
			}
			if plugin.GitHub == "" && plugin.Gist == "" {
				return fmt.Errorf("plugin %q can only set proto for github or gist sources", name)
			}
		}
		if plugin.Inline != "" {
			if err := validateInlinePlugin(name, plugin); err != nil {
				return err
			}
		}
	}
	return nil
}

func validEnvironmentName(name string) bool {
	if name == "" || name[0] != '_' && (name[0] < 'A' || name[0] > 'Z') && (name[0] < 'a' || name[0] > 'z') {
		return false
	}
	for index := 1; index < len(name); index++ {
		character := name[index]
		if character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

// validateInlinePlugin rejects the fields an inline plugin cannot use.
func validateInlinePlugin(name string, plugin RawPlugin) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"proto", plugin.Proto},
		{"rev", plugin.Rev},
		{"branch", plugin.Branch},
		{"tag", plugin.Tag},
		{"dir", plugin.Dir},
		{"file", plugin.File},
	} {
		if field.value != "" {
			return fmt.Errorf("plugin %q cannot set %s for an inline plugin", name, field.name)
		}
	}
	for _, field := range []struct {
		name  string
		count int
	}{
		{"use", len(plugin.Use)},
		{"apply", len(plugin.Apply)},
	} {
		if field.count > 0 {
			return fmt.Errorf("plugin %q cannot set %s for an inline plugin", name, field.name)
		}
	}
	return nil
}

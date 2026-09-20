package config

type Config struct {
	Shell     Shell                `toml:"shell"`
	Matches   []string             `toml:"match"`
	Apply     []string             `toml:"apply"`
	Templates map[string]string    `toml:"templates"`
	Plugins   map[string]RawPlugin `toml:"plugins"`
}

type RawPlugin struct {
	GitHub   string            `toml:"github"`
	Git      string            `toml:"git"`
	Gist     string            `toml:"gist"`
	Remote   string            `toml:"remote"`
	Local    string            `toml:"local"`
	Inline   string            `toml:"inline"`
	Rev      string            `toml:"rev"`
	Branch   string            `toml:"branch"`
	Tag      string            `toml:"tag"`
	Protocol string            `toml:"protocol"`
	Dir      string            `toml:"dir"`
	File     string            `toml:"file"`
	Use      []string          `toml:"use"`
	Apply    []string          `toml:"apply"`
	Profiles []string          `toml:"profiles"`
	Hooks    map[string]string `toml:"hooks"`
}

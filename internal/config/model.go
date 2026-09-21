package config

type Config struct {
	Shell       Shell                `toml:"shell"`
	Matches     []string             `toml:"match"`
	Apply       []string             `toml:"apply"`
	Env         map[string]string    `toml:"env"`
	Templates   map[string]string    `toml:"templates"`
	Plugins     map[string]RawPlugin `toml:"plugins"`
	PluginOrder []string             `toml:"-"`
}

type RawPlugin struct {
	GitHub    string `toml:"github"`
	Git       string `toml:"git"`
	Gist      string `toml:"gist"`
	GitLab    string `toml:"gitlab"`
	Bitbucket string `toml:"bitbucket"`
	Codeberg  string `toml:"codeberg"`
	Remote    string `toml:"remote"`
	Local     string `toml:"local"`
	Optional  bool   `toml:"optional"`
	Inline    string `toml:"inline"`
	Rev       string `toml:"rev"`
	Branch    string `toml:"branch"`
	Tag       string `toml:"tag"`
	Proto     string `toml:"proto"`
	// Protocol is the deprecated spelling of Proto; decoding folds it into Proto.
	Protocol string            `toml:"protocol"`
	Dir      string            `toml:"dir"`
	File     string            `toml:"file"`
	Use      []string          `toml:"use"`
	Apply    []string          `toml:"apply"`
	Build    []string          `toml:"build"`
	Profiles []string          `toml:"profiles"`
	Hooks    map[string]string `toml:"hooks"`
	// CloneOpts are extra arguments passed to git clone for git, github, and gist sources.
	CloneOpts []string `toml:"cloneopts"`
	// Depth sets the git clone depth; nil keeps the shallow depth-1 default, 0 clones full history.
	Depth *int `toml:"depth"`
}

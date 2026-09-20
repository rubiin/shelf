package lock

type Context struct {
	ConfigFile string
	// ConfigFingerprint lets callers that already read the config skip a second read.
	ConfigFingerprint string
	DataDirectory     string
	Profile           string
	Shell             string
	// Templates are the resolved apply templates, recorded so rendering needs no config read.
	Templates map[string]string
}

type Mode int

const (
	ModeNormal Mode = iota
	ModeUpdate
	ModeReinstall
)

type LockedConfig struct {
	ConfigFingerprint string         `toml:"config_fingerprint"`
	Profile           string         `toml:"profile"`
	Shell             string         `toml:"shell"`
	Plugins           []LockedPlugin `toml:"plugins"`
	// Templates must stay last: TOML tables and arrays of tables end the preceeding table.
	Templates map[string]string `toml:"templates,omitempty"`
}

type LockedPlugin struct {
	Name string `toml:"name"`
	// Inline holds an inline plugin's text, which is rendered instead of sourced from files.
	Inline    string            `toml:"inline,omitempty"`
	Source    string            `toml:"source,omitempty"`
	Rev       string            `toml:"rev,omitempty"`
	Directory string            `toml:"directory,omitempty"`
	Files     []string          `toml:"files,omitempty"`
	Apply     []string          `toml:"apply,omitempty"`
	Hooks     map[string]string `toml:"hooks,omitempty"`
}

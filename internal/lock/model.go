package lock

type Context struct {
	ConfigFile string
	// ConfigFingerprint lets callers that already read the config skip a second read.
	ConfigFingerprint string
	DataDirectory     string
	Profile           string
	Shell             string
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
}

type LockedPlugin struct {
	Name      string            `toml:"name"`
	Source    string            `toml:"source,omitempty"`
	Rev       string            `toml:"rev,omitempty"`
	Directory string            `toml:"directory"`
	Files     []string          `toml:"files"`
	Apply     []string          `toml:"apply"`
	Hooks     map[string]string `toml:"hooks"`
}

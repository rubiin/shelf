package lock

type Context struct {
	ConfigFile    string
	DataDirectory string
	Profile       string
	Shell         string
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
	Directory string            `toml:"directory"`
	Files     []string          `toml:"files"`
	Apply     []string          `toml:"apply"`
	Hooks     map[string]string `toml:"hooks"`
}

package lock

import "io"

type Context struct {
	ConfigFile string
	// ConfigFingerprint lets callers that already read the config skip a second read.
	ConfigFingerprint string
	DataDirectory     string
	Profile           string
	Shell             string
	// Templates are the resolved apply templates, recorded so rendering needs no config read.
	Templates map[string]string
	// Diagnostics receives build hook output; nil discards it.
	Diagnostics io.Writer
}

type Mode int

const (
	ModeNormal Mode = iota
	ModeUpdate
	ModeReinstall
)

type LockedConfig struct {
	ConfigFingerprint string            `toml:"config_fingerprint"`
	Profile           string            `toml:"profile"`
	ProfileMatch      string            `toml:"profile_match,omitempty"`
	Shell             string            `toml:"shell"`
	Env               map[string]string `toml:"env,omitempty"`
	Plugins           []LockedPlugin    `toml:"plugins"`
	// Templates must stay last: TOML tables and arrays of tables end the preceeding table.
	Templates map[string]string `toml:"templates,omitempty"`
}

type RevisionManifest struct {
	Plugins []RevisionPlugin `toml:"plugins"`
}

type RevisionPlugin struct {
	Name   string `toml:"name"`
	Source string `toml:"source"`
	Rev    string `toml:"rev"`
}

type LockedPlugin struct {
	Name string `toml:"name"`
	// Inline holds an inline plugin's text, which is rendered instead of sourced from files.
	Inline string `toml:"inline,omitempty"`
	Source string `toml:"source,omitempty"`
	// URL is the resolved clone URL, which lets Restore reinstall the revision from the lock alone.
	URL       string            `toml:"url,omitempty"`
	Rev       string            `toml:"rev,omitempty"`
	Directory string            `toml:"directory,omitempty"`
	Files     []string          `toml:"files,omitempty"`
	Apply     []string          `toml:"apply,omitempty"`
	Hooks     map[string]string `toml:"hooks,omitempty"`
	// CloneOpts and Depth record the clone behavior so Restore reinstalls identically.
	CloneOpts []string `toml:"cloneopts,omitempty"`
	Depth     *int     `toml:"depth,omitempty"`
}

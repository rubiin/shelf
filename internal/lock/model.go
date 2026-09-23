package lock

import "io"

type Context struct {
	ConfigFile string
	// ConfigFingerprint saves callers a second config read.
	ConfigFingerprint string
	DataDirectory     string
	Profile           string
	Shell             string
	// Resolved apply templates, recorded so rendering needs no config read.
	Templates map[string]string
	// PreviousETags from the last lock, keyed by plugin name; installs send them as If-None-Match.
	PreviousETags map[string]string
	// Force refreshes frozen plugins during an update.
	Force bool
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
	// Must stay last: a table or array-of-tables header ends the preceding table.
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
	// Inline is the plugin's text, rendered instead of sourced from files.
	Inline string `toml:"inline,omitempty"`
	Source string `toml:"source,omitempty"`
	// URL is the resolved clone URL, so Restore works from the lock alone.
	URL string `toml:"url,omitempty"`
	Rev string `toml:"rev,omitempty"`
	// ETag lets the next update fetch conditionally and skip an unchanged body.
	ETag      string            `toml:"etag,omitempty"`
	Directory string            `toml:"directory,omitempty"`
	Files     []string          `toml:"files,omitempty"`
	Apply     []string          `toml:"apply,omitempty"`
	Hooks     map[string]string `toml:"hooks,omitempty"`
	// CloneOpts and Depth make Restore clone the same way.
	CloneOpts []string `toml:"cloneopts,omitempty"`
	Depth     *int     `toml:"depth,omitempty"`
	// Frozen keeps the plugin on its pinned version during update.
	Frozen bool `toml:"frozen,omitempty"`
	// Ignore persists the ignore globs for later locks and info.
	Ignore []string `toml:"ignore,omitempty"`
}

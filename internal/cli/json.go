package cli

import (
	"encoding/json"
	"io"
)

// outputFormat selects text or JSON rendering for the query commands.
type outputFormat int

const (
	formatText outputFormat = iota
	formatJSON
)

// selectedFormat maps the shared --json flag to an outputFormat.
func selectedFormat() outputFormat {
	if jsonOutput {
		return formatJSON
	}
	return formatText
}

// pathsPayload is `shelf path --json`; keys match the plain-text field names.
type pathsPayload struct {
	ConfigDir  string `json:"config_dir"`
	DataDir    string `json:"data_dir"`
	ConfigFile string `json:"config_file"`
	LockFile   string `json:"lock_file"`
}

// infoPayload is `shelf info --json`. SizeBytes is a pointer so an empty plugin
// directory still reports 0 bytes rather than dropping the field.
type infoPayload struct {
	Name      string   `json:"name"`
	Source    string   `json:"source,omitempty"`
	Rev       string   `json:"rev,omitempty"`
	Files     []string `json:"files,omitempty"`
	SizeBytes *int64   `json:"size_bytes,omitempty"`
}

// statusPayload is `shelf status --json`; Ok saves callers from parsing State,
// which stays the human-readable detail.
type statusPayload struct {
	Name  string `json:"name"`
	Ok    bool   `json:"ok"`
	State string `json:"state"`
}

// encodeJSON indents and terminates the document. HTML escaping is off so URLs
// and paths stay literal for scripts and editors.
func encodeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

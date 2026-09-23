package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Bare TOML key chars only: dots would nest tables, other chars corrupt the header.
var pluginNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validatePluginName(name string) error {
	if name == "" {
		return fmt.Errorf("plugin name is empty")
	}
	if !pluginNamePattern.MatchString(name) {
		return fmt.Errorf("plugin name %q may only contain letters, digits, dashes, and underscores", name)
	}
	return nil
}

func Add(path, name string, plugin RawPlugin) error {
	if err := validatePluginName(name); err != nil {
		return err
	}
	if err := Validate(Config{Plugins: map[string]RawPlugin{name: plugin}}); err != nil {
		return err
	}
	// Decode doubles as the duplicate check and hands us the contents to edit.
	existing, contents, err := LoadWithContents(path)
	if err != nil {
		return err
	}
	if _, exists := existing.Plugins[name]; exists {
		return fmt.Errorf("plugin %q already exists", name)
	}
	encoded := encodePlugin(name, plugin)
	if len(contents) > 0 && contents[len(contents)-1] != '\n' {
		contents = append(contents, '\n')
	}
	contents = append(contents, []byte("\n"+encoded)...)
	return writeVerified(path, contents)
}

// writeVerified refuses to replace the config unless the new contents are valid.
func writeVerified(path string, contents []byte) error {
	written, err := decode(contents)
	if err != nil {
		return err
	}
	if err := Validate(written); err != nil {
		return err
	}
	return writeAtomically(path, contents)
}

// writeAtomically preserves the file mode and swaps contents via temp file + rename.
func writeAtomically(path string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if info, err := os.Stat(path); err == nil {
		if err := temporary.Chmod(info.Mode().Perm()); err != nil {
			_ = temporary.Close()
			return err
		}
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

// Remove deletes a plugin declared as a table, dotted key, inline table, or quoted name.
func Remove(path, name string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// The line scanner below relies on well-formed TOML (brackets balance, arrays
	// close). Refuse to edit a config that does not decode instead of dropping
	// lines after a bracket that never closes.
	if _, err := decode(contents); err != nil {
		return fmt.Errorf("remove plugin %q: config is invalid: %w", name, err)
	}
	removed := false
	inside := false
	root := true
	pluginsTable := false // inside [plugins], where plugins can be inline tables
	depth := 0            // unmatched [ while dropping a multi-line dotted-key value
	lines := strings.Split(string(contents), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if depth > 0 {
			depth += countBrackets(line)
			if depth <= 0 {
				depth = 0
			}
			removed = true
			continue
		}
		header := normalizeTableHeader(line)
		switch {
		case header != "":
			// After a header, dotted keys belong to that table, not top-level plugins.
			root = false
			pluginsTable = header == "[plugins]"
			inside = isPluginTable(header, name)
			if inside {
				removed = true
				continue
			}
		case root && !inside && isDottedPluginKey(line, name):
			removed = true
			depth = countBrackets(line)
			continue
		case pluginsTable && !inside && isInlineTablePluginKey(line, name):
			removed = true
			continue
		}
		if inside {
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return fmt.Errorf("plugin %q not found", name)
	}
	result := []byte(strings.Join(kept, "\n"))
	if _, err := decode(result); err != nil {
		return fmt.Errorf("remove plugin %q: resulting config is invalid: %w", name, err)
	}
	return writeAtomically(path, result)
}

// countBrackets counts unmatched [, ignoring brackets in strings and comments.
func countBrackets(line string) int {
	inString := byte(0)
	escaped := false
	var opens, closes int
	for index := 0; index < len(line); index++ {
		character := line[index]
		switch {
		case inString == '"':
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = 0
			}
		case inString == '\'':
			if character == '\'' {
				inString = 0
			}
		case character == '#':
			return opens - closes
		case character == '"' || character == '\'':
			inString = character
		case character == '[':
			opens++
		case character == ']':
			closes++
		}
	}
	return opens - closes
}

// normalizeTableHeader strips spaces, quotes, and a trailing comment from a
// header, or "" for other lines.
func normalizeTableHeader(line string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '[' || strings.HasPrefix(trimmed, "[[") {
		return ""
	}
	trimmed = strings.TrimSpace(stripTrailingComment(trimmed))
	if !strings.HasSuffix(trimmed, "]") {
		return ""
	}
	removed := strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "")
	return removed.Replace(trimmed)
}

// stripTrailingComment removes a TOML comment (# ...), ignoring # inside quoted keys.
func stripTrailingComment(line string) string {
	var quote byte
	escaped := false
	for index := 0; index < len(line); index++ {
		character := line[index]
		switch {
		case quote == '"':
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				quote = 0
			}
		case quote == '\'':
			if character == '\'' {
				quote = 0
			}
		case character == '"' || character == '\'':
			quote = character
		case character == '#':
			return line[:index]
		}
	}
	return line
}

// isPluginTable matches the plugin's table and its subtables.
func isPluginTable(header, name string) bool {
	prefix := "[plugins." + name
	return header == prefix+"]" || strings.HasPrefix(header, prefix+".")
}

// isDottedPluginKey matches a plugins.<name>.<field> assignment line.
func isDottedPluginKey(line, name string) bool {
	trimmed := strings.TrimSpace(line)
	assignment := strings.Index(trimmed, "=")
	if assignment < 0 {
		return false
	}
	key := strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "").Replace(trimmed[:assignment])
	return strings.HasPrefix(key, "plugins."+name+".")
}

// isInlineTablePluginKey matches a `name = { ... }` plugin declaration inside [plugins].
func isInlineTablePluginKey(line, name string) bool {
	trimmed := strings.TrimSpace(line)
	assignment := strings.Index(trimmed, "=")
	if assignment < 0 {
		return false
	}
	if !strings.HasPrefix(strings.TrimSpace(trimmed[assignment+1:]), "{") {
		return false
	}
	key := strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "").Replace(trimmed[:assignment])
	return key == name
}

func encodePlugin(name string, plugin RawPlugin) string {
	lines := []string{"[plugins." + name + "]"}
	fields := []struct{ key, value string }{
		{"github", plugin.GitHub}, {"git", plugin.Git}, {"gist", plugin.Gist},
		{"gitlab", plugin.GitLab}, {"bitbucket", plugin.Bitbucket}, {"codeberg", plugin.Codeberg},
		{"remote", plugin.Remote}, {"local", plugin.Local}, {"inline", plugin.Inline},
		{"rev", plugin.Rev}, {"branch", plugin.Branch}, {"tag", plugin.Tag},
		{"proto", plugin.Proto}, {"dir", plugin.Dir}, {"file", plugin.File},
	}
	for _, field := range fields {
		if field.value != "" {
			lines = append(lines, fmt.Sprintf("%s = %q", field.key, field.value))
		}
	}
	if len(plugin.Use) > 0 {
		lines = append(lines, "use = "+tomlArray(plugin.Use))
	}
	if len(plugin.Ignore) > 0 {
		lines = append(lines, "ignore = "+tomlArray(plugin.Ignore))
	}
	if len(plugin.Apply) > 0 {
		lines = append(lines, "apply = "+tomlArray(plugin.Apply))
	}
	if len(plugin.Build) > 0 {
		lines = append(lines, "build = "+tomlArray(plugin.Build))
	}
	if len(plugin.Profiles) > 0 {
		lines = append(lines, "profiles = "+tomlArray(plugin.Profiles))
	}
	if len(plugin.CloneOpts) > 0 {
		lines = append(lines, "cloneopts = "+tomlArray(plugin.CloneOpts))
	}
	if plugin.Depth != nil {
		lines = append(lines, fmt.Sprintf("depth = %d", *plugin.Depth))
	}
	if plugin.Frozen {
		lines = append(lines, "frozen = true")
	}
	if len(plugin.Hooks) > 0 {
		// One subtable, sorted; a repeated header would redefine it.
		keys := make([]string, 0, len(plugin.Hooks))
		for key := range plugin.Hooks {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		lines = append(lines, "[plugins."+name+".hooks]")
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("%s = %q", key, plugin.Hooks[key]))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func tomlArray(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = fmt.Sprintf("%q", value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

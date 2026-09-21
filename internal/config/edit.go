package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// pluginNamePattern allows just bare TOML key characters: dots nest tables and the rest corrupt the header.
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
	// Decoding is the duplicate check: it resolves dotted keys and supplies the contents to edit.
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

// writeVerified replaces path only when the new contents decode and validate, via temp file and rename.
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

// writeAtomically keeps the existing mode and replaces path through a temp file and rename.
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

// Remove deletes a plugin, whether it is declared as a table, a dotted key, or a quoted name.
func Remove(path, name string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	removed := false
	inside := false
	root := true
	lines := strings.Split(string(contents), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		header := normalizeTableHeader(line)
		switch {
		case header != "":
			// A dotted key after any table header belongs to that table, not to a top-level plugin.
			root = false
			inside = isPluginTable(header, name)
			if inside {
				removed = true
				continue
			}
		case root && !inside && isDottedPluginKey(line, name):
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
	return writeAtomically(path, []byte(strings.Join(kept, "\n")))
}

// normalizeTableHeader returns a table header without spaces or quotes, or "" for other lines.
func normalizeTableHeader(line string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' || strings.HasPrefix(trimmed, "[[") {
		return ""
	}
	removed := strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "")
	return removed.Replace(trimmed)
}

// isPluginTable reports whether a normalized header is the plugin's table or one of its subtables.
func isPluginTable(header, name string) bool {
	prefix := "[plugins." + name
	return header == prefix+"]" || strings.HasPrefix(header, prefix+".")
}

// isDottedPluginKey reports whether a line assigns a plugins.<name>.<field> or plugins.<name>.<hook> key.
func isDottedPluginKey(line, name string) bool {
	trimmed := strings.TrimSpace(line)
	assignment := strings.Index(trimmed, "=")
	if assignment < 0 {
		return false
	}
	key := strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "").Replace(trimmed[:assignment])
	return strings.HasPrefix(key, "plugins."+name+".")
}

func encodePlugin(name string, plugin RawPlugin) string {
	lines := []string{"[plugins." + name + "]"}
	fields := []struct{ key, value string }{
		{"github", plugin.GitHub}, {"git", plugin.Git}, {"gist", plugin.Gist},
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
	if len(plugin.Apply) > 0 {
		lines = append(lines, "apply = "+tomlArray(plugin.Apply))
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
	if len(plugin.Hooks) > 0 {
		// Every hook shares one subtable, sorted, since a repeated header would redefine it.
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

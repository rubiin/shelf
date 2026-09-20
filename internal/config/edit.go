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
	written, err := Load(temporaryName)
	if err != nil {
		return err
	}
	if err := Validate(written); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func Remove(path, name string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	target := "[plugins." + name + "]"
	lines := strings.Split(string(contents), "\n")
	start := -1
	end := len(lines)
	for index, line := range lines {
		if line == target {
			start = index
			continue
		}
		if start >= 0 && strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "[plugins."+name+".") {
			end = index
			break
		}
	}
	if start >= 0 {
		lines = append(lines[:start], lines[end:]...)
	}
	updated := []byte(strings.Join(lines, "\n"))
	if string(updated) == string(contents) {
		return fmt.Errorf("plugin %q not found", name)
	}
	return os.WriteFile(path, updated, 0o600)
}

func encodePlugin(name string, plugin RawPlugin) string {
	lines := []string{"[plugins." + name + "]"}
	fields := []struct{ key, value string }{
		{"github", plugin.GitHub}, {"git", plugin.Git}, {"gist", plugin.Gist},
		{"remote", plugin.Remote}, {"local", plugin.Local}, {"inline", plugin.Inline},
		{"rev", plugin.Rev}, {"branch", plugin.Branch}, {"tag", plugin.Tag},
		{"protocol", plugin.Protocol}, {"dir", plugin.Dir}, {"file", plugin.File},
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

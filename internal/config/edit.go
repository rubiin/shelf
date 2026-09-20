package config

import (
	"fmt"
	"os"
	"strings"
)

func Add(path, name string, plugin RawPlugin) error {
	if name == "" {
		return fmt.Errorf("plugin name is empty")
	}
	if err := Validate(Config{Plugins: map[string]RawPlugin{name: plugin}}); err != nil {
		return err
	}
	if _, err := Load(path); err != nil {
		return err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	section := "[plugins." + name + "]"
	if strings.Contains(string(contents), section) {
		return fmt.Errorf("plugin %q already exists", name)
	}
	encoded := encodePlugin(section, plugin)
	if len(contents) > 0 && contents[len(contents)-1] != '\n' {
		contents = append(contents, '\n')
	}
	contents = append(contents, []byte("\n"+encoded)...)
	return os.WriteFile(path, contents, 0o600)
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
		if start >= 0 && strings.HasPrefix(line, "[") {
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

func encodePlugin(section string, plugin RawPlugin) string {
	lines := []string{section}
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
	for name, value := range plugin.Hooks {
		lines = append(lines, fmt.Sprintf("[plugins.%s.hooks]\n%s = %q", strings.Trim(name, "."), name, value))
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

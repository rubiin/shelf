package render

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"shelf/internal/lock"
)

type PluginData struct {
	Name      string
	Directory string
	File      string
	Files     []string
	Hooks     []string
}

func Script(locked lock.LockedConfig, shell string, custom ...map[string]string) (string, error) {
	if shell != "bash" && shell != "zsh" {
		return "", fmt.Errorf("unsupported shell: %s", shell)
	}
	merged := map[string]string{}
	for _, extra := range custom {
		for name, value := range extra {
			merged[name] = value
		}
	}
	for name, value := range BuiltinTemplates(shell) {
		if _, exists := merged[name]; !exists {
			merged[name] = value
		}
	}

	var output strings.Builder
	for _, plugin := range locked.Plugins {
		data := PluginData{Name: plugin.Name, Directory: plugin.Directory, Files: plugin.Files}
		hookNames := make([]string, 0, len(plugin.Hooks))
		for name := range plugin.Hooks {
			hookNames = append(hookNames, name)
		}
		sort.Strings(hookNames)
		for _, name := range hookNames {
			hook := plugin.Hooks[name]
			line, err := Template(name, hook, data)
			if err != nil {
				return "", err
			}
			output.WriteString(line)
			output.WriteString("\n")
		}

		apply := plugin.Apply
		if len(apply) == 0 {
			apply = []string{"source"}
		}
		for _, name := range apply {
			text, ok := merged[name]
			if !ok {
				return "", fmt.Errorf("unknown template: %s", name)
			}
			if strings.Contains(text, "{file}") || name == "source" {
				for _, file := range plugin.Files {
					data.File = file
					if strings.HasSuffix(file, "inline.sh") {
						contents, err := os.ReadFile(file)
						if err != nil {
							return "", err
						}
						output.Write(contents)
						if len(contents) == 0 || contents[len(contents)-1] != '\n' {
							output.WriteString("\n")
						}
						continue
					}
					line, err := Template(name, text, data)
					if err != nil {
						return "", err
					}
					output.WriteString(line)
					output.WriteString("\n")
				}
				continue
			}
			line, err := Template(name, text, data)
			if err != nil {
				return "", err
			}
			output.WriteString(line)
			output.WriteString("\n")
		}
	}
	return output.String(), nil
}

func Template(name, text string, data PluginData) (string, error) {
	result := strings.NewReplacer(
		"{name}", data.Name,
		"{dir}", data.Directory,
		"{file}", data.File,
		"{nl}", "\n",
	).Replace(text)
	return result, nil
}

func defaultTemplate(shell string) string {
	if shell == "zsh" {
		return "source {file}"
	}
	return "source {file}"
}

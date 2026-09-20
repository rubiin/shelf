package render

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"shelf/internal/lock"
)

type PluginData struct {
	Name      string
	Directory string
	File      string
	Files     []string
	Hooks     map[string]string
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
		data := PluginData{Name: plugin.Name, Directory: plugin.Directory, Files: plugin.Files, Hooks: plugin.Hooks}
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
			line = normalizeRenderedOutput(line)
			output.WriteString(line)
			if line != "" && !strings.HasSuffix(line, "\n") {
				output.WriteString("\n")
			}
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
					line = normalizeRenderedOutput(line)
					output.WriteString(line)
					if line != "" && !strings.HasSuffix(line, "\n") {
						output.WriteString("\n")
					}
				}
				continue
			}
			line, err := Template(name, text, data)
			if err != nil {
				return "", err
			}
			line = normalizeRenderedOutput(line)
			output.WriteString(line)
			if line != "" && !strings.HasSuffix(line, "\n") {
				output.WriteString("\n")
			}
		}
	}
	return output.String(), nil
}

func Template(name, text string, data PluginData) (string, error) {
	result, err := expandTemplateLoops(text, data)
	if err != nil {
		return "", err
	}
	return expandTemplateExpressions(result, data)
}

func expandTemplateLoops(text string, data PluginData) (string, error) {
	for {
		start := strings.Index(text, "{%")
		if start == -1 {
			return text, nil
		}
		end := strings.Index(text[start+2:], "%}")
		if end == -1 {
			return text, nil
		}
		tag := strings.TrimSpace(text[start+2 : start+2+end])
		if !strings.HasPrefix(tag, "for ") {
			return text, nil
		}
		parts := strings.Fields(strings.TrimSpace(strings.TrimPrefix(tag, "for ")))
		if len(parts) != 3 || parts[1] != "in" {
			return "", fmt.Errorf("invalid loop tag: %s", tag)
		}
		varName := parts[0]
		iterName := parts[2]
		loopStart := start + 2 + end + 2
		closeMarker := "{% endfor %}"
		closeIdx := strings.Index(text[loopStart:], closeMarker)
		if closeIdx == -1 {
			return "", fmt.Errorf("unclosed loop in template: %q", text)
		}
		body := text[loopStart : loopStart+closeIdx]
		items, err := resolveTemplateIterable(iterName, data)
		if err != nil {
			return "", err
		}
		var rendered strings.Builder
		for _, item := range items {
			local := data
			if varName == "file" {
				local.File = item
			}
			expanded, err := expandTemplateLoops(body, local)
			if err != nil {
				return "", err
			}
			resolved, err := expandTemplateExpressions(expanded, local)
			if err != nil {
				return "", err
			}
			rendered.WriteString(resolved)
		}
		text = text[:start] + rendered.String() + text[loopStart+closeIdx+len(closeMarker):]
	}
}

func resolveTemplateIterable(name string, data PluginData) ([]string, error) {
	switch name {
	case "files":
		return data.Files, nil
	case "hooks":
		keys := make([]string, 0, len(data.Hooks))
		for key := range data.Hooks {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return keys, nil
	default:
		return nil, fmt.Errorf("unknown template iterable: %s", name)
	}
}

var (
	expressionPattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)
	blankLinesPattern = regexp.MustCompile(`\n{3,}`)
)

func expandTemplateExpressions(text string, data PluginData) (string, error) {
	if !strings.Contains(text, "{{") {
		return placeholderReplacer(data).Replace(text), nil
	}

	var result strings.Builder
	last := 0
	for _, match := range expressionPattern.FindAllStringSubmatchIndex(text, -1) {
		result.WriteString(text[last:match[0]])
		value, err := resolveTemplateValue(text[match[2]:match[3]], data)
		if err != nil {
			return "", err
		}
		result.WriteString(value)
		last = match[1]
	}
	result.WriteString(text[last:])
	return result.String(), nil
}

func placeholderReplacer(data PluginData) *strings.Replacer {
	return strings.NewReplacer(
		"{name}", data.Name,
		"{dir}", data.Directory,
		"{file}", data.File,
		"{nl}", "\n",
	)
}

func resolveTemplateValue(expr string, data PluginData) (string, error) {
	expr = strings.TrimSpace(expr)
	filter := ""
	if parts := strings.SplitN(expr, "|", 2); len(parts) == 2 {
		expr = strings.TrimSpace(parts[0])
		filter = strings.TrimSpace(parts[1])
	}
	var value string
	switch expr {
	case "name":
		value = data.Name
	case "dir":
		value = data.Directory
	case "file":
		value = data.File
	case "hooks?.pre", "hooks.pre":
		value = data.Hooks["pre"]
	case "hooks?.post", "hooks.post":
		value = data.Hooks["post"]
	case "hooks?.pre | nl", "hooks.pre | nl":
		value = data.Hooks["pre"]
		filter = "nl"
	default:
		if strings.HasPrefix(expr, "hooks?") {
			key := strings.TrimPrefix(expr, "hooks?")
			key = strings.TrimPrefix(key, ".")
			value = data.Hooks[key]
		}
		if value == "" && strings.HasPrefix(expr, "hooks.") {
			key := strings.TrimPrefix(expr, "hooks.")
			value = data.Hooks[key]
		}
	}
	if filter == "nl" {
		return value + "\n", nil
	}
	return value, nil
}

func normalizeRenderedOutput(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimLeft(text, "\n")
	text = strings.TrimRight(text, "\n")
	return blankLinesPattern.ReplaceAllString(text, "\n\n")
}

func defaultTemplate(shell string) string {
	if shell == "zsh" {
		return "source {file}"
	}
	return "source {file}"
}

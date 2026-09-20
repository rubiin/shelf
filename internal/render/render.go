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

const loopCloseMarker = "{% endfor %}"

func expandTemplateLoops(text string, data PluginData) (string, error) {
	var output strings.Builder
	last := 0
	for {
		start := strings.Index(text[last:], "{%")
		if start == -1 {
			break
		}
		start += last
		end := strings.Index(text[start+2:], "%}")
		if end == -1 {
			break
		}
		tag := strings.TrimSpace(text[start+2 : start+2+end])
		if !strings.HasPrefix(tag, "for ") {
			break
		}
		parts := strings.Fields(strings.TrimSpace(strings.TrimPrefix(tag, "for ")))
		if len(parts) != 3 || parts[1] != "in" {
			return "", fmt.Errorf("invalid loop tag: %s", tag)
		}
		loopStart := start + 2 + end + 2
		closeIdx := strings.Index(text[loopStart:], loopCloseMarker)
		if closeIdx == -1 {
			return "", fmt.Errorf("unclosed loop in template: %q", text)
		}
		items, err := resolveTemplateIterable(parts[2], data)
		if err != nil {
			return "", err
		}
		body := compileTemplateBody(text[loopStart : loopStart+closeIdx])
		output.WriteString(text[last:start])
		for _, item := range items {
			local := data
			if parts[0] == "file" {
				local.File = item
			}
			rendered, err := body.render(local)
			if err != nil {
				return "", err
			}
			output.WriteString(rendered)
		}
		last = loopStart + closeIdx + len(loopCloseMarker)
	}
	output.WriteString(text[last:])
	return output.String(), nil
}

// templateSegment is one piece of a compiled loop body: literal text or a single expression.
type templateSegment struct {
	literal string
	expr    string
}

// templateBody is a loop body compiled once; a body with another {% loop %} expands per item.
type templateBody struct {
	raw      string
	segments []templateSegment
	nested   bool
	plain    bool
}

func compileTemplateBody(body string) templateBody {
	if strings.Contains(body, "{%") {
		return templateBody{raw: body, nested: true}
	}
	matches := expressionPattern.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return templateBody{raw: body, plain: true}
	}
	segments := make([]templateSegment, 0, len(matches)*2+1)
	last := 0
	for _, match := range matches {
		segments = append(segments, templateSegment{literal: body[last:match[0]]})
		segments = append(segments, templateSegment{expr: body[match[2]:match[3]]})
		last = match[1]
	}
	segments = append(segments, templateSegment{literal: body[last:]})
	return templateBody{segments: segments}
}

func (body templateBody) render(data PluginData) (string, error) {
	if body.nested {
		expanded, err := expandTemplateLoops(body.raw, data)
		if err != nil {
			return "", err
		}
		return expandTemplateExpressions(expanded, data)
	}
	if body.plain {
		return expandPlaceholders(body.raw, data), nil
	}
	var rendered strings.Builder
	for _, segment := range body.segments {
		if segment.expr == "" {
			rendered.WriteString(segment.literal)
			continue
		}
		value, err := resolveTemplateValue(segment.expr, data)
		if err != nil {
			return "", err
		}
		rendered.WriteString(value)
	}
	return rendered.String(), nil
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

var expressionPattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

func expandTemplateExpressions(text string, data PluginData) (string, error) {
	if !strings.Contains(text, "{{") {
		return expandPlaceholders(text, data), nil
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

// expandPlaceholders substitutes {name}, {dir}, {file}, and {nl} without building a Replacer.
func expandPlaceholders(text string, data PluginData) string {
	if !strings.ContainsRune(text, '{') {
		return text
	}
	var output strings.Builder
	output.Grow(len(text) + 32)
	for index := 0; index < len(text); {
		if text[index] != '{' {
			next := strings.IndexByte(text[index:], '{')
			if next == -1 {
				output.WriteString(text[index:])
				break
			}
			output.WriteString(text[index : index+next])
			index += next
			continue
		}
		switch {
		case strings.HasPrefix(text[index:], "{name}"):
			output.WriteString(data.Name)
			index += len("{name}")
		case strings.HasPrefix(text[index:], "{dir}"):
			output.WriteString(data.Directory)
			index += len("{dir}")
		case strings.HasPrefix(text[index:], "{file}"):
			output.WriteString(data.File)
			index += len("{file}")
		case strings.HasPrefix(text[index:], "{nl}"):
			output.WriteByte('\n')
			index += len("{nl}")
		default:
			output.WriteByte(text[index])
			index++
		}
	}
	return output.String()
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
	if !strings.Contains(text, "\n\n\n") {
		return text
	}
	var output strings.Builder
	output.Grow(len(text))
	newlines := 0
	for index := 0; index < len(text); index++ {
		if text[index] == '\n' {
			newlines++
			if newlines > 2 {
				continue
			}
		} else {
			newlines = 0
		}
		output.WriteByte(text[index])
	}
	return output.String()
}

// defaultTemplate returns the built-in apply template; bash and zsh source files the same way.
func defaultTemplate(string) string { return "source {file}" }

package render

import (
	"fmt"
	"sort"
	"strings"

	"shelf/internal/lock"
)

// PluginData holds the values one plugin's templates can reference.
type PluginData struct {
	Name      string
	Directory string
	File      string
	Files     []string
	Hooks     map[string]string
}

// ResolveTemplates layers configured templates over the shell's built-in ones.
func ResolveTemplates(shell string, extra ...map[string]string) map[string]string {
	templates := map[string]string{}
	for name, value := range BuiltinTemplates(shell) {
		templates[name] = value
	}
	for _, custom := range extra {
		for name, value := range custom {
			templates[name] = value
		}
	}
	return templates
}

// Script renders the shell code for a locked config, applying each plugin's templates.
func Script(locked lock.LockedConfig, shell string, custom ...map[string]string) (string, error) {
	if shell != "bash" && shell != "zsh" {
		return "", fmt.Errorf("unsupported shell: %s", shell)
	}
	// Templates resolved at lock time are authoritative, so they are used as they are.
	merged := locked.Templates
	if len(merged) == 0 || len(custom) > 0 {
		merged = ResolveTemplates(shell, custom...)
		for name, value := range locked.Templates {
			merged[name] = value
		}
	}

	var output scriptBuffer
	// A rough output estimate saves the buffer from growing one chunk at a time.
	output.Grow(64 * len(locked.Plugins))
	for _, name := range sortedEnvironmentNames(locked.Env) {
		// Assignments run in the current shell, so they are written directly without an extra parse pass per variable.
		output.WriteString(name)
		output.WriteString("=")
		output.WriteString(locked.Env[name])
		output.WriteString("\n")
	}
	// One plugin scope is reused for the whole script: it is reset per plugin, not reallocated.
	var current scope
	for _, plugin := range locked.Plugins {
		var pluginOutput scriptBuffer
		// A plugin chunk is rarely empty, so start its buffer with room to grow.
		pluginOutput.Grow(128)
		// An inline plugin's own text is the template, rendered with just its name and hooks.
		if plugin.Inline != "" {
			if err := renderInline(plugin, shell, &pluginOutput); err != nil {
				return "", err
			}
		} else {
			apply := plugin.Apply
			if len(apply) == 0 {
				apply = []string{"source"}
			}
			// The scope is shared by every template the plugin applies.
			current = scope{plugin: pluginData(plugin), hasPlugin: true}
			for _, name := range apply {
				text, exists := merged[name]
				if !exists {
					return "", fmt.Errorf("unknown template: %s", name)
				}
				// An empty template (zsh's zcompile is empty under bash) contributes nothing.
				if text == "" {
					continue
				}
				if err := renderChunk(name, text, &current, &pluginOutput); err != nil {
					return "", err
				}
			}
		}
		// Each plugin is evaluated separately, so a whole-script `eval "$(shelf source)"` parses one plugin at a time and aliases stay real.
		output.WriteString("eval ")
		output.WriteString(quoteShell(pluginOutput.String()))
		output.WriteString("\n")
	}
	return output.String(), nil
}

func sortedEnvironmentNames(environment map[string]string) []string {
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func quoteShell(text string) string {
	return "'" + strings.ReplaceAll(text, "'", "'\\''") + "'"
}

// renderChunk renders one template, appending a newline when the chunk does not end with one.
func renderChunk(name, text string, current *scope, output *scriptBuffer) error {
	before := output.Len()
	if err := renderTemplateText(name, text, current, output); err != nil {
		return err
	}
	finishChunk(before, output)
	return nil
}

// renderInline renders an inline plugin's text: bash evals it directly, while zsh sources it over stdin because zsh parses an eval'd string whole and would lose aliases defined on earlier lines.
func renderInline(plugin lock.LockedPlugin, shell string, output *scriptBuffer) error {
	before := output.Len()
	current := pluginScope(PluginData{Name: plugin.Name, Hooks: plugin.Hooks})
	var rendered scriptBuffer
	if err := renderCompiledTemplate(plugin.Name, plugin.Inline, current, &rendered); err != nil {
		return err
	}
	finishChunk(0, &rendered)
	if shell != "zsh" {
		output.WriteString(rendered.String())
	} else {
		delimiter := heredocDelimiter(rendered.String())
		output.WriteString("source /dev/stdin <<'")
		output.WriteString(delimiter)
		output.WriteString("'\n")
		output.WriteString(rendered.String())
		output.WriteString(delimiter)
		output.WriteString("\n")
	}
	finishChunk(before, output)
	return nil
}

// heredocDelimiter picks a heredoc end marker that cannot appear as a full line of the quoted-heredoc text.
func heredocDelimiter(text string) string {
	for index := 0; ; index++ {
		delimiter := fmt.Sprintf("SHELF_%d", index)
		if !containsLine(text, delimiter) {
			return delimiter
		}
	}
}

// containsLine reports whether text contains a full line equal to the given line.
func containsLine(text, line string) bool {
	for _, candidate := range strings.Split(text, "\n") {
		if candidate == line {
			return true
		}
	}
	return false
}

// finishChunk appends a newline when a rendered chunk is empty or does not end with one.
func finishChunk(before int, output *scriptBuffer) {
	if output.Len() == before || output.Last() != '\n' {
		output.WriteString("\n")
	}
}

// pluginData converts a locked plugin into the values its templates can reference.
func pluginData(plugin lock.LockedPlugin) PluginData {
	return PluginData{Name: plugin.Name, Directory: plugin.Directory, Files: plugin.Files, Hooks: plugin.Hooks}
}

// renderTemplateText renders a template body, expanding `{file}` templates once per file.
func renderTemplateText(name, text string, current *scope, output *scriptBuffer) error {
	if strings.Contains(text, "{{") || strings.Contains(text, "{%") {
		return renderCompiledTemplate(name, text, current, output)
	}
	if !strings.Contains(text, "{file}") {
		output.WriteString(expandPlaceholders(text, current.plugin))
		return nil
	}
	data := current.plugin
	for _, file := range data.Files {
		data.File = file
		rendered := normalizeRenderedOutput(expandPlaceholders(text, data))
		if rendered != "" && !strings.HasSuffix(rendered, "\n") {
			rendered += "\n"
		}
		output.WriteString(rendered)
	}
	return nil
}

// renderCompiledTemplate renders a parsed template into the script buffer.
func renderCompiledTemplate(name, text string, current *scope, output *scriptBuffer) error {
	nodes, err := compileTemplate(text)
	if err != nil {
		return fmt.Errorf("compile template %q: %w", name, err)
	}
	if err := renderNodes(nodes, current, output); err != nil {
		return fmt.Errorf("render template %q: %w", name, err)
	}
	return nil
}

// Template renders a template body with the plugin's values, caching the parsed template.
func Template(name, text string, data PluginData) (string, error) {
	if !strings.Contains(text, "{{") && !strings.Contains(text, "{%") {
		return expandPlaceholders(text, data), nil
	}
	var output scriptBuffer
	if err := renderCompiledTemplate(name, text, pluginScope(data), &output); err != nil {
		return "", err
	}
	return output.String(), nil
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

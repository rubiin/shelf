package render

import (
	"fmt"
	"sort"
	"strings"

	"shelf/internal/lock"
)

// PluginData is what one plugin's templates can reference.
type PluginData struct {
	Name      string
	Directory string
	File      string
	Files     []string
	Hooks     map[string]string
}

// ResolveTemplates returns the shell's built-ins overridden by extra templates.
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

// Script renders a locked config into shell code.
func Script(locked lock.LockedConfig, shell string, custom ...map[string]string) (string, error) {
	if shell != "bash" && shell != "zsh" {
		return "", fmt.Errorf("unsupported shell: %s", shell)
	}
	// Lock-time templates win over built-ins and custom ones.
	merged := locked.Templates
	if len(merged) == 0 || len(custom) > 0 {
		merged = ResolveTemplates(shell, custom...)
		for name, value := range locked.Templates {
			merged[name] = value
		}
	}

	// Render the plugins first so defer usage is detected from what actually calls the
	// scheduler, not from the literal apply name: a custom template or inline plugin can
	// invoke _shelf_defer under any name.
	var scripts scriptBuffer
	scripts.Grow(64 * len(locked.Plugins))
	var current scope
	for _, plugin := range locked.Plugins {
		var pluginOutput scriptBuffer
		pluginOutput.Grow(128)
		// An inline plugin's text is its own template, with only name and hooks available.
		if plugin.Inline != "" {
			if err := renderInline(plugin, shell, &pluginOutput); err != nil {
				return "", err
			}
		} else {
			apply := plugin.Apply
			if len(apply) == 0 {
				apply = []string{"source"}
			}
			// One scope serves all templates the plugin applies.
			current = scope{plugin: pluginData(plugin), hasPlugin: true}
			for _, name := range apply {
				text, exists := merged[name]
				if !exists {
					return "", fmt.Errorf("unknown template: %s", name)
				}
				// Empty templates (zcompile under bash) contribute nothing.
				if text == "" {
					continue
				}
				if err := renderChunk(name, text, &current, &pluginOutput); err != nil {
					return "", err
				}
			}
		}
		// Eval per plugin: a single whole-script parse would not pick up aliases defined along the way.
		scripts.WriteString("eval ")
		scripts.WriteString(quoteShell(pluginOutput.String()))
		scripts.WriteString("\n")
	}

	var output scriptBuffer
	output.Grow(scripts.Len() + 64*len(locked.Env))
	for _, name := range sortedEnvironmentNames(locked.Env) {
		// Env assignments run directly in the current shell, so they need no eval wrapper.
		output.WriteString(name)
		output.WriteString("=")
		output.WriteString(locked.Env[name])
		output.WriteString("\n")
	}
	// The defer template queues sources into the scheduler defined here, so it must be emitted
	// first. Bash and non-interactive runs degrade to plain source: no scheduler.
	if shell == "zsh" && callsDefer(&scripts) {
		output.WriteString(shelfDeferPreamble)
	}
	output.WriteString(scripts.String())
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

// callsDefer reports whether the rendered plugin scripts invoke the _shelf_defer scheduler,
// which must therefore be defined before them. Checking the rendered output covers custom
// templates and inline plugins that call it under a name other than "defer".
func callsDefer(scripts *scriptBuffer) bool {
	return strings.Contains(scripts.String(), "_shelf_defer")
}

func quoteShell(text string) string {
	return "'" + strings.ReplaceAll(text, "'", "'\\''") + "'"
}

func renderChunk(name, text string, current *scope, output *scriptBuffer) error {
	before := output.Len()
	if err := renderTemplateText(name, text, current, output); err != nil {
		return err
	}
	finishChunk(before, output)
	return nil
}

// renderInline: bash evals the text directly; zsh sources it over stdin because zsh parses an
// eval'd string as a whole and would lose aliases defined on earlier lines.
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

// heredocDelimiter picks a marker that is not a full line of the text.
func heredocDelimiter(text string) string {
	for index := 0; ; index++ {
		delimiter := fmt.Sprintf("SHELF_%d", index)
		if !containsLine(text, delimiter) {
			return delimiter
		}
	}
}

func containsLine(text, line string) bool {
	for _, candidate := range strings.Split(text, "\n") {
		if candidate == line {
			return true
		}
	}
	return false
}

func finishChunk(before int, output *scriptBuffer) {
	if output.Len() == before || output.Last() != '\n' {
		output.WriteString("\n")
	}
}

func pluginData(plugin lock.LockedPlugin) PluginData {
	return PluginData{Name: plugin.Name, Directory: plugin.Directory, Files: plugin.Files, Hooks: plugin.Hooks}
}

// renderTemplateText expands {file} once per file.
func renderTemplateText(name, text string, current *scope, output *scriptBuffer) error {
	if strings.Contains(text, "{{") || strings.Contains(text, "{%") {
		return renderCompiledTemplate(name, text, current, output)
	}
	if !strings.Contains(text, "{file}") {
		output.WriteString(expandPlaceholders(text, current.plugin))
		return nil
	}
	data := current.plugin
	if len(data.Files) == 0 {
		// No files to expand {file} per file, but the chunk's static text still matters:
		// render it with the placeholder left empty rather than dropping the chunk.
		rendered := normalizeRenderedOutput(expandPlaceholders(text, data))
		if rendered != "" && !strings.HasSuffix(rendered, "\n") {
			rendered += "\n"
		}
		output.WriteString(rendered)
		return nil
	}
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

// Template renders text with the plugin's values.
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

// expandPlaceholders handles {name}, {dir}, {file}, and {nl} without a Replacer.
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

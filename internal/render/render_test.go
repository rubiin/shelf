package render

import (
	"strings"
	"testing"

	"shelf/internal/lock"
)

func TestScriptSourcesLockedFiles(t *testing.T) {
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{Name: "demo", Files: []string{"/tmp/demo.sh"}}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "source /tmp/demo.sh") {
		t.Fatalf("script = %q", script)
	}
}

func TestTemplateExpandsFile(t *testing.T) {
	result, err := Template("source {file}", "source {file}", PluginData{File: "/tmp/demo.zsh"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "source /tmp/demo.zsh" {
		t.Fatalf("result = %q", result)
	}
}

func TestTemplateExpandsPluginValuesAndNewlineFilter(t *testing.T) {
	template := "{{ name }} {{ dir }} {{ file }}\n{{ hooks?.pre | nl }}"
	result, err := Template(template, template, PluginData{
		Name:      "demo",
		Directory: "/tmp/demo",
		File:      "/tmp/demo/plugin.zsh",
		Hooks:     map[string]string{"pre": "echo pre"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "demo /tmp/demo /tmp/demo/plugin.zsh\necho pre\n" {
		t.Fatalf("result = %q", result)
	}
}

func TestScriptIncludesPluginHooks(t *testing.T) {
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Hooks: map[string]string{"post": "echo hooked"},
	}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "echo hooked") {
		t.Fatalf("hook output missing from script: %q", script)
	}
}

func TestScriptRendersHooksInStableOrder(t *testing.T) {
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Hooks: map[string]string{"post": "echo post", "pre": "echo pre"},
	}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if script != "echo post\necho pre\n" {
		t.Fatalf("script = %q", script)
	}
}

func TestScriptUsesPluginApplyTemplates(t *testing.T) {
	templates := map[string]string{
		"defer": "echo deferred {name}",
	}
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/demo.sh"},
		Apply: []string{"defer"},
	}}}, "bash", templates)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "echo deferred demo") {
		t.Fatalf("apply template output missing from script: %q", script)
	}
	if strings.Contains(script, "source /tmp/demo.sh") {
		t.Fatalf("script should not fall back to source when apply is defer: %q", script)
	}
}

func TestScriptRejectsUnknownApplyTemplate(t *testing.T) {
	_, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Apply: []string{"missing"},
	}}}, "bash")
	if err == nil || !strings.Contains(err.Error(), "unknown template: missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestTemplateExpandsBarePlaceholders(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "all placeholders", text: "{name}|{dir}|{file}|{nl}", want: "demo|/tmp/demo|/tmp/demo/demo.zsh|\n"},
		{name: "repeated", text: "{file} {file}", want: "/tmp/demo/demo.zsh /tmp/demo/demo.zsh"},
		{name: "unknown brace", text: "{unknown}", want: "{unknown}"},
		{name: "unclosed brace", text: "{name", want: "{name"},
		{name: "trailing brace", text: "name}", want: "name}"},
		{name: "empty braces", text: "{}", want: "{}"},
	}
	data := PluginData{Name: "demo", Directory: "/tmp/demo", File: "/tmp/demo/demo.zsh"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Template(test.name, test.text, data)
			if err != nil {
				t.Fatal(err)
			}
			if result != test.want {
				t.Fatalf("result = %q, want %q", result, test.want)
			}
		})
	}
}

func TestNormalizeRenderedOutput(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "windows newlines", text: "a\r\nb", want: "a\nb"},
		{name: "leading newlines", text: "\n\n\na", want: "a"},
		{name: "trailing newlines", text: "a\n\n", want: "a"},
		{name: "blank line run collapses", text: "a\n\n\n\n\nb", want: "a\n\nb"},
		{name: "two newlines kept", text: "a\n\nb", want: "a\n\nb"},
		{name: "runs split by text", text: "a\n\n\nb\n\n\n\nc", want: "a\n\nb\n\nc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeRenderedOutput(test.text); got != test.want {
				t.Fatalf("normalizeRenderedOutput(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}

func TestTemplateRejectsNestedLoops(t *testing.T) {
	// Loops do not nest: the outer body ends at the first {% endfor %}, so inner loops fail to close.
	_, err := Template("nested", "{% for file in files %}{% for hook in hooks %}{{ hook }}{% endfor %}{% endfor %}", PluginData{
		Files: []string{"/tmp/one.zsh"},
		Hooks: map[string]string{"pre": "echo pre"},
	})
	if err == nil {
		t.Fatal("nested loops were accepted")
	}
}

func TestTemplateLeavesLiteralTextUntouched(t *testing.T) {
	// Bare placeholders are only substituted when the text carries no {{ expressions }}.
	result, err := Template("mixed", "source {file} {{ name }}", PluginData{Name: "demo", File: "/tmp/demo.zsh"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "source {file} demo" {
		t.Fatalf("result = %q", result)
	}
}

func TestScriptExpandsHookTemplateLoops(t *testing.T) {
	template := "{{ hooks?.pre | nl }}{% for file in files %}zsh-defer source \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/one.zsh", "/tmp/two.zsh"},
		Apply: []string{"defer"},
		Hooks: map[string]string{"pre": "echo pre", "post": "echo post"},
	}}}, "zsh", map[string]string{"defer": template})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "echo pre") || !strings.Contains(script, "echo post") {
		t.Fatalf("hook template output missing from script: %q", script)
	}
	if !strings.Contains(script, "zsh-defer source \"/tmp/one.zsh\"") || !strings.Contains(script, "zsh-defer source \"/tmp/two.zsh\"") {
		t.Fatalf("defer loop output missing from script: %q", script)
	}
	if strings.Contains(script, "{{ hooks?.pre") || strings.Contains(script, "{% for file in files %}") {
		t.Fatalf("template syntax was left literal in script: %q", script)
	}
	if strings.Contains(script, "\n\n\n") {
		t.Fatalf("script contains excessive blank lines: %q", script)
	}
}

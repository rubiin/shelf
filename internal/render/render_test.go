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

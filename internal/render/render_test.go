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

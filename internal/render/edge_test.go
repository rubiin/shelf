package render

import (
	"strings"
	"testing"

	"shelf/internal/lock"
)

// Script-level edge cases that the happy-path tests never reach: unsupported
// shells, inline templates that fail to render, empty apply templates, the
// lock/custom template merge, per-file {file} expansion, and render errors.

func TestScriptRejectsUnsupportedShell(t *testing.T) {
	if _, err := Script(lock.LockedConfig{}, "fish"); err == nil || !strings.Contains(err.Error(), "unsupported shell: fish") {
		t.Fatalf("error = %v, want an unsupported shell error", err)
	}
}

func TestScriptForwardsInlineTemplateErrors(t *testing.T) {
	if _, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{Name: "broken", Inline: "{{ missing }}"}}}, "zsh"); err == nil || !strings.Contains(err.Error(), `unknown template value "missing"`) {
		t.Fatalf("error = %v, want the inline render error", err)
	}
}

func TestScriptSkipsEmptyApplyTemplates(t *testing.T) {
	// A lock recording an empty template (zcompile under bash) contributes nothing.
	script, err := Script(lock.LockedConfig{
		Templates: map[string]string{"zcompile": ""},
		Plugins:   []lock.LockedPlugin{{Name: "demo", Files: []string{"/tmp/demo.zsh"}, Apply: []string{"zcompile"}}},
	}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if want := "eval ''\n"; script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

func TestScriptOverlaysLockedTemplatesOnCustomOnes(t *testing.T) {
	locked := lock.LockedConfig{
		Templates: map[string]string{"source": "echo from-lock"},
		Plugins:   []lock.LockedPlugin{{Name: "demo", Apply: []string{"source"}}},
	}
	script, err := Script(locked, "bash", map[string]string{"source": "echo custom"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "eval 'echo from-lock\n'\n"; script != want {
		t.Fatalf("script = %q, want the locked template to win %q", script, want)
	}
}

func TestScriptExpandsFilePlaceholdersAcrossSelectedFiles(t *testing.T) {
	script, err := Script(lock.LockedConfig{
		Plugins: []lock.LockedPlugin{{Name: "demo", Files: []string{"/tmp/a.sh", "/tmp/b.sh"}, Apply: []string{"source"}}},
	}, "bash", map[string]string{"source": "echo {file}"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "eval 'echo /tmp/a.sh\necho /tmp/b.sh\n'\n"; script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

func TestScriptForwardsApplyTemplateRenderErrors(t *testing.T) {
	if _, err := Script(lock.LockedConfig{
		Plugins: []lock.LockedPlugin{{Name: "demo", Files: []string{"/tmp/a.sh"}, Apply: []string{"source"}}},
	}, "bash", map[string]string{"source": "source {{ missing }}"}); err == nil || !strings.Contains(err.Error(), `unknown template value "missing"`) {
		t.Fatalf("error = %v, want the apply template render error", err)
	}
}

// Template engine branches: value printing and truthiness, which the parser
// reaches only through template text.

func TestTemplatePrintBranches(t *testing.T) {
	result, err := Template("print", "{{ true }}|{{ 42 }}", PluginData{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "true|42"; result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
	// Maps and lists have no printable value.
	for _, text := range []string{"{{ hooks }}", "{{ files }}"} {
		if _, err := Template("print", text, PluginData{Hooks: map[string]string{"pre": "x"}, Files: []string{"/tmp/a.zsh"}}); err == nil || !strings.Contains(err.Error(), "not printable") {
			t.Fatalf("template %q: error = %v, want a not-printable error", text, err)
		}
	}
}

func TestTemplateTruthyBranches(t *testing.T) {
	tests := []struct {
		name     string
		template string
		data     PluginData
		want     string
	}{
		{name: "nonempty number", template: "{% if 7 %}yes{% else %}no{% endif %}", want: "yes"},
		{name: "empty texts", template: "{% if files %}yes{% else %}no{% endif %}", want: "no"},
		{name: "nonempty texts", template: "{% if files %}yes{% else %}no{% endif %}", data: PluginData{Files: []string{"/tmp/a.zsh"}}, want: "yes"},
		{name: "nonempty map", template: "{% if hooks %}yes{% else %}no{% endif %}", data: PluginData{Hooks: map[string]string{"pre": "x"}}, want: "yes"},
		{name: "loop value", template: "{% for f in files %}{% if loop %}yes{% endif %}{% endfor %}", data: PluginData{Files: []string{"/tmp/a.zsh", "/tmp/b.zsh"}}, want: "yesyes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Template(test.name, test.template, test.data)
			if err != nil {
				t.Fatal(err)
			}
			if result != test.want {
				t.Fatalf("result = %q, want %q", result, test.want)
			}
		})
	}
}

func TestTemplateEvaluatesParenthesizedExpressions(t *testing.T) {
	// Bare names parse straight to lookups, so the parenthesized fallback only
	// runs for expressions the planner leaves generic.
	result, err := Template("parens", "{{ (name) }}", PluginData{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "demo" {
		t.Fatalf("result = %q, want %q", result, "demo")
	}
}

func TestTemplateRejectsEmptyAndMalformedExpressions(t *testing.T) {
	for _, test := range []struct {
		name     string
		template string
		data     PluginData
		what     string
	}{
		{name: "empty expression", template: "{{ }}", what: "empty template expression"},
		{name: "not recursion error", template: "{{ not missing }}", what: `unknown template value "missing"`},
		{name: "bad string literal", template: `{{ "a\x" }}`, what: "invalid syntax"},
	} {
		if _, err := Template(test.name, test.template, test.data); err == nil || !strings.Contains(err.Error(), test.what) {
			t.Fatalf("template %q: error = %v, want an error containing %q", test.template, err, test.what)
		}
	}
}

func TestTemplateIfAndForRenderErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		template string
		data     PluginData
		what     string
	}{
		{name: "if condition", template: "{% if missing %}x{% endif %}", what: `unknown template value "missing"`},
		{name: "for iterable", template: "{% for x in missing %}x{% endfor %}", what: `unknown template value "missing"`},
		{name: "list arity", template: "{% for a, b in files %}x{% endfor %}", data: PluginData{Files: []string{"/tmp/a.zsh"}}, what: "a list loop takes one variable"},
		{name: "list body", template: "{% for f in files %}{{ missing }}{% endfor %}", data: PluginData{Files: []string{"/tmp/a.zsh"}}, what: `unknown template value "missing"`},
		{name: "map body", template: "{% for k, v in hooks %}{{ missing }}{% endfor %}", data: PluginData{Hooks: map[string]string{"pre": "x"}}, what: `unknown template value "missing"`},
		{name: "not iterable", template: "{% for x in 42 %}x{% endfor %}", what: "cannot loop over this value"},
	} {
		if _, err := Template(test.name, test.template, test.data); err == nil || !strings.Contains(err.Error(), test.what) {
			t.Fatalf("template %q: error = %v, want an error containing %q", test.template, err, test.what)
		}
	}
}

func TestTemplateRejectsStrayEndBlocks(t *testing.T) {
	for _, text := range []string{"{% endif %}", "{% endfor %}", "{% else %}"} {
		if _, err := Template("end", text, PluginData{}); err == nil {
			t.Errorf("template %q was accepted", text)
		}
	}
}

func TestTemplateRejectsMalformedBlocks(t *testing.T) {
	for _, test := range []struct {
		name     string
		template string
		data     PluginData
	}{
		{name: "unknown block in if", template: "{% if name %}{% bogus %}{% endif %}"},
		{name: "empty for", template: "{% for %}x{% endfor %}"},
		{name: "for without in", template: "{% for x %}x{% endfor %}"},
		{name: "empty for variable", template: "{% for a, in files %}x{% endfor %}", data: PluginData{Files: []string{"/tmp/a.zsh"}}},
		{name: "unknown block in for", template: "{% for f in files %}{% bogus %}{% endfor %}", data: PluginData{Files: []string{"/tmp/a.zsh"}}},
	} {
		if _, err := Template(test.name, test.template, test.data); err == nil {
			t.Errorf("template %q was accepted", test.template)
		}
	}
}

func TestTemplateNestedLoopsReuseLoopState(t *testing.T) {
	// The outer loop's body is itself a loop that reads loop.*, so the planner
	// must detect the inner use and label both loops as using loop state.
	result, err := Template("nested", "{% for a in files %}{% for b in files %}{{ loop.index }}{% endfor %}{% endfor %}", PluginData{Files: []string{"/tmp/a.zsh", "/tmp/b.zsh"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "0101"; result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
}

func TestCallFunctionNlAndGet(t *testing.T) {
	for _, test := range []struct {
		name     string
		template string
		data     PluginData
		want     string
		what     string
	}{
		{name: "nl appends newline", template: `{{ nl("text") }}`, want: "text\n"},
		{name: "nl wrong arity", template: "{{ nl() }}", what: "nl takes one argument"},
		{name: "nl argument error", template: "{{ nl(missing) }}", what: `unknown template value "missing"`},
		{name: "get wrong arity", template: "{{ get(hooks) }}", data: PluginData{Hooks: map[string]string{"pre": "x"}}, what: "get takes two arguments"},
		{name: "get container error", template: `{{ get(missing, "pre") }}`, what: `unknown template value "missing"`},
		{name: "get key error", template: "{{ get(hooks, missing) }}", data: PluginData{Hooks: map[string]string{"pre": "x"}}, what: `unknown template value "missing"`},
		{name: "get non-map container", template: `{{ get(files, "0") }}`, data: PluginData{Files: []string{"/tmp/a.zsh"}}, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := Template(test.name, test.template, test.data)
			switch {
			case test.what != "" && (err == nil || !strings.Contains(err.Error(), test.what)):
				t.Fatalf("result = %q, error = %v, want an error containing %q", result, err, test.what)
			case test.what == "" && err != nil:
				t.Fatalf("error = %v, want result %q", err, test.want)
			case test.what == "" && result != test.want:
				t.Fatalf("result = %q, want %q", result, test.want)
			}
		})
	}
}

func TestTemplateUnknownFunctionAndFilter(t *testing.T) {
	if _, err := Template("fn", "{{ frobnicate(name) }}", PluginData{Name: "demo"}); err == nil || !strings.Contains(err.Error(), `unknown template function "frobnicate"`) {
		t.Fatalf("error = %v, want an unknown function error", err)
	}
	if _, err := Template("filter", "{{ name | bogus }}", PluginData{Name: "demo"}); err == nil || !strings.Contains(err.Error(), `unknown template filter "bogus"`) {
		t.Fatalf("error = %v, want an unknown filter error", err)
	}
	// dquote on a map fails when the value is not printable.
	if _, err := Template("dquote", "{{ hooks | dquote }}", PluginData{Hooks: map[string]string{"pre": "x"}}); err == nil || !strings.Contains(err.Error(), "not printable") {
		t.Fatalf("error = %v, want a not-printable error", err)
	}
}

func TestTemplateLoopMemberFields(t *testing.T) {
	result, err := Template("last", "{% for f in files %}{{ loop.last }};{% endfor %}", PluginData{Files: []string{"/tmp/a.zsh", "/tmp/b.zsh"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "false;true;"; result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
	if _, err := Template("bad member", "{% for f in files %}{{ loop.foo }}{% endfor %}", PluginData{Files: []string{"/tmp/a.zsh"}}); err == nil || !strings.Contains(err.Error(), "has no field") {
		t.Fatalf("error = %v, want a missing-field error", err)
	}
}

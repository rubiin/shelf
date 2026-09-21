package render

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"shelf/internal/lock"
)

func TestScriptSourcesLockedFiles(t *testing.T) {
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{Name: "demo", Files: []string{"/tmp/demo.sh"}}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if script != "eval 'source \"/tmp/demo.sh\"\n'\n" {
		t.Fatalf("script = %q", script)
	}
}

func TestScriptAssignsEnvironmentBeforePlugins(t *testing.T) {
	script, err := Script(lock.LockedConfig{
		Env:     map[string]string{"plugins": "(git npm macos)", "ZSH_THEME": "robbyrussell"},
		Plugins: []lock.LockedPlugin{{Name: "demo", Inline: "print -r -- \"$ZSH_THEME:$plugins\""}},
	}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("zsh", "-fc", `eval "$1"`, "shelf-test", script).CombinedOutput()
	if err != nil {
		t.Fatalf("zsh eval failed: %v\n%s", err, output)
	}
	if string(output) != "robbyrussell:git npm macos\n" {
		t.Fatalf("zsh output = %q", output)
	}
}

func TestScriptRendersHooksAroundTheSourcedFiles(t *testing.T) {
	// The built-in source template renders the hook map inside the apply template, pre before post.
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/one.zsh", "/tmp/two.zsh"},
		Hooks: map[string]string{"pre": "echo pre\n", "post": "echo post"},
	}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	want := "eval 'echo pre\nsource \"/tmp/one.zsh\"\nsource \"/tmp/two.zsh\"\necho post\n'\n"
	if script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

func TestScriptMakesAliasesAvailableAcrossPluginsWhenEvaluated(t *testing.T) {
	directory := t.TempDir()
	aliasFile := filepath.Join(directory, "alias.zsh")
	if err := os.WriteFile(aliasFile, []byte("alias aliastest='echo aliastest'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{
		{Name: "alias", Files: []string{aliasFile}},
		{Name: "command", Inline: "aliastest\n"},
	}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command("zsh", "-fc", `eval "$1"`, "shelf-test", script).CombinedOutput()
	if err != nil {
		t.Fatalf("zsh eval failed: %v\n%s", err, output)
	}
	if string(output) != "aliastest\n" {
		t.Fatalf("zsh output = %q, want %q", output, "aliastest\n")
	}
}

func TestScriptMakesInlineAliasesAvailableToInlineFunctions(t *testing.T) {
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:   "inline",
		Inline: "alias aliastest='echo aliastest'\nfunctest() { aliastest; }\nfunctest\n",
	}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command("zsh", "-fc", `eval "$1"`, "shelf-test", script).CombinedOutput()
	if err != nil {
		t.Fatalf("zsh eval failed: %v\n%s", err, output)
	}
	if string(output) != "aliastest\n" {
		t.Fatalf("zsh output = %q, want %q", output, "aliastest\n")
	}
}

func TestBuiltinTemplates(t *testing.T) {
	bash := BuiltinTemplates("bash")
	if bash["PATH"] != "export PATH=\"{{ dir }}:$PATH\"" {
		t.Errorf("bash PATH = %q", bash["PATH"])
	}
	for _, name := range []string{"path", "fpath"} {
		if _, exists := bash[name]; exists {
			t.Errorf("bash must not define %s", name)
		}
	}
	zsh := BuiltinTemplates("zsh")
	for name, want := range map[string]string{
		"PATH":  "export PATH=\"{{ dir }}:$PATH\"",
		"path":  "path=( \"{{ dir }}\" $path )",
		"fpath": "fpath=( \"{{ dir }}\" $fpath )",
	} {
		if zsh[name] != want {
			t.Errorf("zsh %s = %q, want %q", name, zsh[name], want)
		}
	}

	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{Name: "demo", Directory: "/tmp/demo", Apply: []string{"path"}}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if script != "eval 'path=( \"/tmp/demo\" $path )\n'\n" {
		t.Fatalf("path script = %q", script)
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

func TestScriptRendersHooksInTemplateOrder(t *testing.T) {
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Hooks: map[string]string{"post": "echo post", "pre": "echo pre"},
	}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if script != "eval 'echo pre\necho post\n'\n" {
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

func TestTemplateRendersNestedBlocks(t *testing.T) {
	template := "{% for file in files %}{% if loop.first %}first {% else %}more {% endif %}{{ loop.index }}:{{ file }};{% endfor %}"
	result, err := Template("nested", template, PluginData{Files: []string{"/tmp/one.zsh", "/tmp/two.zsh"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "first 0:/tmp/one.zsh;more 1:/tmp/two.zsh;"; result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
}

func TestTemplateRendersConditionals(t *testing.T) {
	template := "{% if hooks?.pre %}{{ hooks.pre | nl }}{% else if hooks?.post %}post{% else %}none{% endif %}"
	tests := []struct {
		name  string
		hooks map[string]string
		want  string
	}{
		{name: "if branch", hooks: map[string]string{"pre": "echo pre"}, want: "echo pre\n"},
		{name: "else if branch", hooks: map[string]string{"post": "echo post"}, want: "post"},
		{name: "else branch", hooks: map[string]string{}, want: "none"},
		{name: "empty value is falsy", hooks: map[string]string{"pre": ""}, want: "none"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Template("conditional", template, PluginData{Hooks: test.hooks})
			if err != nil {
				t.Fatal(err)
			}
			if result != test.want {
				t.Fatalf("result = %q, want %q", result, test.want)
			}
		})
	}
}

func TestTemplateIteratesMapsByKeyAndValue(t *testing.T) {
	template := "{% for name, value in hooks %}{{ name }}={{ value }};{% endfor %}"
	result, err := Template("hooks", template, PluginData{Hooks: map[string]string{"pre": "echo pre", "post": "echo post"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "post=echo post;pre=echo pre;"; result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
}

func TestTemplateRejectsSingleVariableMapLoops(t *testing.T) {
	if _, err := Template("map loop", "{% for hook in hooks %}{{ hook }}{% endfor %}", PluginData{Hooks: map[string]string{"pre": "echo pre"}}); err == nil {
		t.Fatal("a map loop with one variable was accepted")
	}
}

func TestTemplateLookupsAreStrictUnlessOptional(t *testing.T) {
	if _, err := Template("strict", "{{ missing }}", PluginData{}); err == nil {
		t.Fatal("an unknown name was accepted")
	}
	if _, err := Template("field", "{{ hooks.pre }}", PluginData{Hooks: map[string]string{}}); err == nil {
		t.Fatal("an unknown field was accepted")
	}
	result, err := Template("optional", "[{{ hooks?.pre }}]", PluginData{Hooks: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if result != "[]" {
		t.Fatalf("result = %q, want []", result)
	}
}

func TestTemplateSupportsGetAndNot(t *testing.T) {
	template := "{% if not hooks?.pre %}none{% endif %}{{ get(hooks, \"pre\") }}"
	result, err := Template("get", template, PluginData{Hooks: map[string]string{"pre": "echo pre"}})
	if err != nil {
		t.Fatal(err)
	}
	if result != "echo pre" {
		t.Fatalf("result = %q, want the hook value", result)
	}
}

func TestTemplateRejectsUnsupportedBlocks(t *testing.T) {
	for _, template := range []string{
		"{% include \"other\" %}",
		"{% if hooks?.pre %}unclosed",
		"{% for file in files %}unclosed",
		"{{ unclosed",
	} {
		if _, err := Template("bad", template, PluginData{Files: []string{"/tmp/one.zsh"}}); err == nil {
			t.Errorf("template %q was accepted", template)
		}
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

func TestScriptRendersInlinePluginThroughTheTemplateEngine(t *testing.T) {
	locked := lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:   "greeting",
		Inline: "echo {{ name }}\n{{ hooks?.pre | nl }}{{ hooks?.post | nl }}",
		Hooks:  map[string]string{"pre": "echo pre", "post": "echo post"},
	}}}
	script, err := Script(locked, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if want := "eval 'source /dev/stdin <<'\\''SHELF_0'\\''\necho greeting\necho pre\necho post\nSHELF_0\n'\n"; script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

func TestScriptRendersBashInlineTextWithoutSourcing(t *testing.T) {
	// bash parses an eval'd string as one unit, so rendering the inline text straight into the per-plugin eval skips the fork per inline plugin.
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:   "greeting",
		Inline: "echo {{ name }}\n{{ hooks?.pre | nl }}{{ hooks?.post | nl }}",
		Hooks:  map[string]string{"pre": "echo pre", "post": "echo post"},
	}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if want := "eval 'echo greeting\necho pre\necho post\n'\n"; script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

func TestScriptPicksAnInlineHeredocDelimiterThatDoesNotCollide(t *testing.T) {
	// The inline text already contains a line equal to SHELF_0, so the heredoc must fall back.
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:   "inline",
		Inline: "echo above\nSHELF_0\necho below\n",
	}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if want := "eval 'source /dev/stdin <<'\\''SHELF_1'\\''\necho above\nSHELF_0\necho below\nSHELF_1\n'\n"; script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

func TestScriptMakesInlineAliasesAvailableToInlineFunctionsInBash(t *testing.T) {
	// bash expands aliases only when the option is on and reads an eval'd string a line at a time, so the fork-free bash shape must keep aliases real by function-body read time.
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:   "inline",
		Inline: "alias aliastest='echo aliastest'\nfunctest() { aliastest; }\nfunctest\n",
	}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("bash", "-c", `shopt -s expand_aliases; eval "$1"`, "shelf-test", script).CombinedOutput()
	if err != nil {
		t.Fatalf("bash eval failed: %v\n%s", err, output)
	}
	if string(output) != "aliastest\n" {
		t.Fatalf("bash output = %q, want %q", output, "aliastest\n")
	}
}

func TestScriptRendersFromTheTemplatesRecordedInTheLock(t *testing.T) {
	// The lock records the resolved templates, so rendering needs nothing from the config.
	locked := lock.LockedConfig{
		Templates: map[string]string{"source": "source \"{{ files.0 }}\" # recorded"},
		Plugins: []lock.LockedPlugin{{
			Name:  "demo",
			Files: []string{"/tmp/demo.zsh"},
			Apply: []string{"source"},
		}},
	}
	script, err := Script(locked, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if want := "eval 'source \"/tmp/demo.zsh\" # recorded\n'\n"; script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

func TestExpressionPlansMatchTheFullEvaluator(t *testing.T) {
	// A planned expression must resolve exactly like the general evaluator, errors included.
	current := pluginScope(PluginData{
		Name:      "demo",
		Directory: "/plugins/demo",
		File:      "/plugins/demo/demo.zsh",
		Files:     []string{"/plugins/demo/one.zsh", "/plugins/demo/two.zsh"},
		Hooks:     map[string]string{"pre": "echo pre"},
	})
	describe := func(value templateValue) string {
		return fmt.Sprintf("kind=%d text=%q number=%d flag=%v texts=%v members=%d", value.kind, value.text, value.number, value.flag, value.texts, len(value.dict))
	}
	for _, expression := range []string{
		"name", "dir", "file", "files", "files.0", "files.1", "files.2", "hooks", "hooks?.pre", "hooks?.missing", "hooks.pre",
		"name | nl", "hooks?.pre | nl", "files.0 | nl", "missing | nl", "unknown.thing | nl", "not name", "?.name",
		"missing", "missing.member", "true", "false", "42", "\"literal\"", "get(hooks, \"pre\")", "name.member", "files.0.member",
	} {
		planned, plannedErr := planExpression(expression).evaluate(expression, current)
		generic, genericErr := evalExpression(expression, current)
		switch {
		case (plannedErr == nil) != (genericErr == nil):
			t.Errorf("%q: planned error = %v, generic error = %v", expression, plannedErr, genericErr)
		case plannedErr != nil:
			if plannedErr.Error() != genericErr.Error() {
				t.Errorf("%q: planned error = %q, generic error = %q", expression, plannedErr, genericErr)
			}
		case describe(planned) != describe(generic):
			t.Errorf("%q: planned = %s, generic = %s", expression, describe(planned), describe(generic))
		}
	}
}

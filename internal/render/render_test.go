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

func TestScriptDoesNotSourceIgnoredFiles(t *testing.T) {
	// The ignore globs already dropped the test file from the lock's selection, so neither shell renders it.
	tests := []struct {
		shell string
		want  string
	}{
		{shell: "bash", want: "eval 'source \"/tmp/demo.plugin.zsh\"\n'\n"},
		{shell: "zsh", want: "eval 'source \"/tmp/demo.plugin.zsh\"\n'\n"},
	}
	for _, test := range tests {
		t.Run(test.shell, func(t *testing.T) {
			script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
				Name:  "demo",
				Files: []string{"/tmp/demo.plugin.zsh"},
			}}}, test.shell)
			if err != nil {
				t.Fatal(err)
			}
			if script != test.want {
				t.Fatalf("%s script = %q, want %q", test.shell, script, test.want)
			}
		})
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
	// zcompile degrades to the plain source template under bash, so one apply list loads files
	// in both shells (bash just never compiles anything).
	if bash["zcompile"] != bash["source"] {
		t.Errorf("bash zcompile = %q, want it to match the source template %q", bash["zcompile"], bash["source"])
	}
	// defer degrades the same way: bash has no prompt-time deferral, so the files just load now.
	if bash["defer"] != bash["source"] {
		t.Errorf("bash defer = %q, want it to match the source template %q", bash["defer"], bash["source"])
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
	if zsh["zcompile"] != zcompileTemplate {
		t.Errorf("zsh zcompile = %q, want the guard template", zsh["zcompile"])
	}
	if zsh["defer"] != deferTemplate {
		t.Errorf("zsh defer = %q, want the queueing defer template", zsh["defer"])
	}

	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{Name: "demo", Directory: "/tmp/demo", Apply: []string{"path"}}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if script != "eval 'path=( \"/tmp/demo\" $path )\n'\n" {
		t.Fatalf("path script = %q", script)
	}
}

// TestScriptRendersZcompileGuardBeforeEachSourcedFile checks that the built-in zcompile template
// is self-sufficient like the defer template: one guarded compile line ahead of each source line,
// with the plugin hooks rendered around the loop, inside one eval chunk.
func TestScriptRendersZcompileGuardBeforeEachSourcedFile(t *testing.T) {
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/one.zsh", "/tmp/two.zsh"},
		Hooks: map[string]string{"pre": "echo pre\n", "post": "echo post"},
		Apply: []string{"zcompile"},
	}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	want := "eval 'echo pre\n" +
		"[[ ! -e \"/tmp/one.zsh.zwc\" || \"/tmp/one.zsh.zwc\" -ot \"/tmp/one.zsh\" ]] && zcompile \"/tmp/one.zsh\"\n" +
		"source \"/tmp/one.zsh\"\n" +
		"[[ ! -e \"/tmp/two.zsh.zwc\" || \"/tmp/two.zsh.zwc\" -ot \"/tmp/two.zsh\" ]] && zcompile \"/tmp/two.zsh\"\n" +
		"source \"/tmp/two.zsh\"\n" +
		"echo post\n'\n"
	if script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

// TestScriptTreatsZcompileAsNoOpInBash checks that under bash the same apply list renders each
// file as a plain source line with no compile text, byte-identical to applying the source template.
func TestScriptTreatsZcompileAsNoOpInBash(t *testing.T) {
	compiled, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/one.zsh"},
		Apply: []string{"zcompile"},
	}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/one.zsh"},
		Apply: []string{"source"},
	}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if compiled != plain {
		t.Fatalf("bash zcompile output = %q, want the plain source output %q", compiled, plain)
	}
	want := "eval 'source \"/tmp/one.zsh\"\n'\n"
	if compiled != want {
		t.Fatalf("bash output = %q, want %q", compiled, want)
	}
}

// TestScriptRendersDeferredSourceBeforeEachFile checks that the built-in defer template queues
// one source per file with the embedded scheduler, whose definition precedes the deferred chunk,
// and that the hooks stay immediate.
func TestScriptRendersDeferredSourceBeforeEachFile(t *testing.T) {
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/one.zsh", "/tmp/two.zsh"},
		Hooks: map[string]string{"pre": "echo pre\n", "post": "echo post"},
		Apply: []string{"defer"},
	}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	want := shelfDeferPreamble + "eval 'echo pre\n" +
		"_shelf_defer source \"/tmp/one.zsh\"\n" +
		"_shelf_defer source \"/tmp/two.zsh\"\n" +
		"echo post\n'\n"
	if script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

// TestScriptTreatsDeferAsNoOpInBash checks that under bash the same apply list renders each
// file as a plain source line, byte-identical to applying the source template.
func TestScriptTreatsDeferAsNoOpInBash(t *testing.T) {
	deferred, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/one.zsh"},
		Apply: []string{"defer"},
	}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/one.zsh"},
		Apply: []string{"source"},
	}}}, "bash")
	if err != nil {
		t.Fatal(err)
	}
	if deferred != plain {
		t.Fatalf("bash defer output = %q, want the plain source output %q", deferred, plain)
	}
	want := "eval 'source \"/tmp/one.zsh\"\n'\n"
	if deferred != want {
		t.Fatalf("bash output = %q, want %q", deferred, want)
	}
}

// TestScriptDeferSourcesAtFirstIdle runs the rendered script in a real zsh, proving the queued
// deferred files are sourced in order when the first idle pass drains the queue (the same queue
// the zle -F handler drains after the first prompt draws).
func TestScriptDeferSourcesAtFirstIdle(t *testing.T) {
	directory := t.TempDir()
	one := filepath.Join(directory, "one.zsh")
	two := filepath.Join(directory, "two.zsh")
	if err := os.WriteFile(one, []byte("print -r -- one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, []byte("print -r -- two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{one, two},
		Apply: []string{"defer"},
	}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	// Eval the script (defines the scheduler, queues the sources) then drain the queue the way
	// _shelf_defer_idle does when zle first goes idle. Non-interactive zsh never draws a prompt,
	// so draining directly is the faithful stand-in for the first idle pass.
	output, err := exec.Command("zsh", "-fc", `eval "$1"; _shelf_defer_drain`, "shelf-test", script).CombinedOutput()
	if err != nil {
		t.Fatalf("zsh eval failed: %v\n%s", err, output)
	}
	if string(output) != "one\ntwo\n" {
		t.Fatalf("zsh output = %q, want %q", output, "one\ntwo\n")
	}
}

// TestScriptZcompileGuardCompilesAndSkipsFresh runs the rendered script in a real zsh, checking
// the first load bootstraps a .zwc and a second load leaves it untouched (self-maintaining on mtime).
func TestScriptZcompileGuardCompilesAndSkipsFresh(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "demo.plugin.zsh")
	if err := os.WriteFile(file, []byte("print -r -- compiled\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{file},
		Apply: []string{"zcompile"},
	}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	evalScript := func(script string) string {
		t.Helper()
		output, err := exec.Command("zsh", "-fc", `eval "$1"`, "shelf-test", script).CombinedOutput()
		if err != nil {
			t.Fatalf("zsh eval failed: %v\n%s", err, output)
		}
		return string(output)
	}
	if output := evalScript(script); output != "compiled\n" {
		t.Fatalf("first zsh output = %q, want %q", output, "compiled\n")
	}
	zwc := file + ".zwc"
	first, err := os.Stat(zwc)
	if err != nil {
		t.Fatalf("first load did not zcompile a .zwc: %v", err)
	}
	if output := evalScript(script); output != "compiled\n" {
		t.Fatalf("second zsh output = %q, want %q", output, "compiled\n")
	}
	second, err := os.Stat(zwc)
	if err != nil {
		t.Fatalf(".zwc disappeared after the second load: %v", err)
	}
	if !second.ModTime().Equal(first.ModTime()) {
		t.Fatalf("fresh .zwc was recompiled: mtime changed from %v to %v", first.ModTime(), second.ModTime())
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

// TestScriptQuotesFilePathsSoTheLiteralFileIsSourced checks that a filename containing $, `,
// or \ is escaped inside the double quotes before it reaches the shell. Without the fix the
// source line would expand the character and load a different (nonexistent) file.
func TestScriptQuotesFilePathsSoTheLiteralFileIsSourced(t *testing.T) {
	for _, name := range []string{"a$b.sh", "a`b.sh", `a\b.sh`} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			file := filepath.Join(directory, name)
			if err := os.WriteFile(file, []byte("echo Sourced\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, shell := range []string{"bash", "zsh"} {
				t.Run(shell, func(t *testing.T) {
					script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
						Name:  "demo",
						Files: []string{file},
					}}}, shell)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(script, `source "`+file+`"`) {
						t.Fatalf("%s: path emitted unescaped inside double quotes: %q", shell, script)
					}
					var output []byte
					if shell == "bash" {
						output, err = exec.Command("bash", "-c", `eval "$1"`, "shelf-test", script).CombinedOutput()
					} else {
						output, err = exec.Command("zsh", "-fc", `eval "$1"`, "shelf-test", script).CombinedOutput()
					}
					if err != nil {
						t.Fatalf("%s eval failed: %v\n%s", shell, err, output)
					}
					if string(output) != "Sourced\n" {
						t.Fatalf("%s output = %q, want %q", shell, output, "Sourced\n")
					}
				})
			}
		})
	}
}

// TestScriptDeferQuotesFilePathsSoTheLiteralFileIsSourced checks that the deferred queue
// sources the exact file too: the escaped path keeps the literal $, and the scheduler's
// ${(q)} re-quotes it for the drain's eval.
func TestScriptDeferQuotesFilePathsSoTheLiteralFileIsSourced(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "a$b.sh")
	if err := os.WriteFile(file, []byte("echo Deferred\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{file},
		Apply: []string{"defer"},
	}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("zsh", "-fc", `eval "$1"; _shelf_defer_drain`, "shelf-test", script).CombinedOutput()
	if err != nil {
		t.Fatalf("zsh eval failed: %v\n%s", err, output)
	}
	if string(output) != "Deferred\n" {
		t.Fatalf("zsh output = %q, want %q", output, "Deferred\n")
	}
}

// TestScriptEmitsDeferPreambleWhenCustomCodeCallsTheScheduler checks that the scheduler is
// defined whenever the rendered script actually calls _shelf_defer, no matter the apply name:
// a custom template or an inline plugin can invoke it without ever naming "defer".
func TestScriptEmitsDeferPreambleWhenCustomCodeCallsTheScheduler(t *testing.T) {
	for _, test := range []struct {
		name   string
		plugin lock.LockedPlugin
	}{
		{name: "custom template", plugin: lock.LockedPlugin{
			Name:  "demo",
			Files: []string{"/tmp/one.zsh"},
			Apply: []string{"queue"},
		}},
		{name: "inline plugin", plugin: lock.LockedPlugin{
			Name:   "demo",
			Inline: "_shelf_defer source \"/tmp/one.zsh\"\n",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{test.plugin}}, "zsh",
				map[string]string{"queue": "_shelf_defer source \"/tmp/one.zsh\""})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(script, shelfDeferPreamble) {
				t.Fatalf("script missing the defer preamble: %q", script)
			}
			if !strings.Contains(script, "_shelf_defer source") {
				t.Fatalf("script missing the deferred call: %q", script)
			}
		})
	}
}

// TestScriptKeepsStaticTextWhenAPluginSelectsNoFiles checks that a {file}-style template on a
// plugin with zero selected files still renders its static text instead of dropping it.
func TestScriptKeepsStaticTextWhenAPluginSelectsNoFiles(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
				Name:  "demo",
				Apply: []string{"source"},
			}}}, shell, map[string]string{"source": "echo static {file}"})
			if err != nil {
				t.Fatal(err)
			}
			if want := "eval 'echo static \n'\n"; script != want {
				t.Fatalf("%s script = %q, want %q", shell, script, want)
			}
		})
	}
}

// TestTemplateKeepsTagDelimitersInsideStringLiterals checks that tokenize closes a tag at the
// real delimiter, not at a "}}" or "%}" that appears inside a quoted string literal.
func TestTemplateKeepsTagDelimitersInsideStringLiterals(t *testing.T) {
	for _, test := range []struct {
		name string
		text string
		data PluginData
		want string
	}{
		{name: "expression end delimiter", text: `{{ "}}" }}`, want: "}}"},
		{name: "escaped quote inside expression", text: `{{ "a\"}}" }}`, want: `a"}}`},
		{name: "block end delimiter", text: `{% if "%}" %}yes{% endif %}`, want: "yes"},
		{name: "expression after a literal", text: `{{ name }} {{ "}}" }}`, data: PluginData{Name: "demo"}, want: "demo }}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := Template(test.name, test.text, test.data)
			if err != nil {
				t.Fatal(err)
			}
			if result != test.want {
				t.Fatalf("result = %q, want %q", result, test.want)
			}
		})
	}
}

// TestTemplateRejectsEmptyIfCondition checks that an if with no condition errors instead of
// rendering its body unconditionally.
func TestTemplateRejectsEmptyIfCondition(t *testing.T) {
	for _, template := range []string{
		"{% if %}yes{% endif %}",
		"{% if   %}yes{% endif %}",
		"{% if %}{% endif %}",
		"{% if hooks?.pre %}pre{% else if %}empty{% endif %}",
	} {
		if _, err := Template("if", template, PluginData{}); err == nil {
			t.Errorf("template %q was accepted", template)
		}
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
	script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{
		Name:  "demo",
		Files: []string{"/tmp/one.zsh", "/tmp/two.zsh"},
		Apply: []string{"defer"},
		Hooks: map[string]string{"pre": "echo pre", "post": "echo post"},
	}}}, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "echo pre") || !strings.Contains(script, "echo post") {
		t.Fatalf("hook template output missing from script: %q", script)
	}
	if !strings.Contains(script, "_shelf_defer source \"/tmp/one.zsh\"") || !strings.Contains(script, "_shelf_defer source \"/tmp/two.zsh\"") {
		t.Fatalf("defer queue output missing from script: %q", script)
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
		"name | nl", "hooks?.pre | nl", "files.0 | nl", "file | dquote", "missing | nl", "unknown.thing | nl", "not name", "?.name",
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

// TestScriptDeferRearmsForASecondSourceInTheSameSession walks the scheduler through two
// full arm/setup/idle cycles in one zsh, as a second `shelf source` in an interactive
// session would: the second batch must re-arm the precmd hook and fd and then drain.
func TestScriptDeferRearmsForASecondSourceInTheSameSession(t *testing.T) {
	directory := t.TempDir()
	render := func(name string) string {
		t.Helper()
		file := filepath.Join(directory, name+".zsh")
		if err := os.WriteFile(file, []byte("print -r -- "+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		script, err := Script(lock.LockedConfig{Plugins: []lock.LockedPlugin{{Name: name, Files: []string{file}, Apply: []string{"defer"}}}}, "zsh")
		if err != nil {
			t.Fatal(err)
		}
		return script
	}
	// One cycle: the prompt runs the precmd hook, then zle goes idle and calls the handler.
	cycle := `(( ${+_shelf_defer_fd} )) || print -r -- unarmed
[[ -n ${precmd_functions[(r)_shelf_defer_setup]} ]] || print -r -- no-hook
_shelf_defer_setup
_shelf_defer_idle
`
	command := `set -u; eval "$1"` + "\n" + cycle + `eval "$2"` + "\n" + cycle
	output, err := exec.Command("zsh", "-fc", command, "shelf-test", render("first"), render("second")).CombinedOutput()
	if err != nil {
		t.Fatalf("zsh eval failed: %v\n%s", err, output)
	}
	if string(output) != "first\nsecond\n" {
		t.Fatalf("zsh output = %q, want %q", output, "first\nsecond\n")
	}
}

package render

import (
	"testing"

	"shelf/internal/lock"
)

func benchLocked() lock.LockedConfig {
	plugins := make([]lock.LockedPlugin, 0, 7)
	names := []string{"zsh-defer", "zsh-vi-mode", "powerlevel10k", "fast-syntax-highlighting", "evalcache", "zsh-autosuggestions", "zsh-you-should-use"}
	for _, name := range names {
		plugins = append(plugins, lock.LockedPlugin{
			Name:      name,
			Directory: "/home/u/.local/share/shelf/plugins/" + name,
			Files:     []string{"/home/u/.local/share/shelf/plugins/" + name + "/" + name + ".plugin.zsh"},
			Apply:     []string{"source"},
		})
	}
	plugins[2].Apply = []string{"source"}
	plugins[1].Apply = []string{"source"}
	plugins[1].Hooks = nil
	return lock.LockedConfig{Shell: "zsh", Plugins: plugins}
}

func BenchmarkScriptPlainSource(b *testing.B) {
	locked := benchLocked()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Script(locked, "zsh"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScriptDeferTemplateLoops(b *testing.B) {
	locked := benchLocked()
	locked.Plugins[0].Apply = []string{"defer"}
	locked.Plugins[3].Apply = []string{"defer"}
	locked.Plugins[5].Apply = []string{"defer"}
	locked.Plugins[6].Apply = []string{"defer"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Script(locked, "zsh", map[string]string{"defer": deferTemplate}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTemplateExpressions(b *testing.B) {
	data := PluginData{Name: "demo", Directory: "/plugins/demo", File: "/plugins/demo/demo.zsh"}
	text := "source \"{{ file }}\"\nexport PATH=\"{{ dir }}:$PATH\"\n# {{ name }} loaded"
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Template("bench", text, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTemplateConditionals(b *testing.B) {
	data := PluginData{Name: "demo", Directory: "/plugins/demo", Files: []string{"/plugins/demo/a.zsh", "/plugins/demo/b.zsh"}, Hooks: map[string]string{"pre": "echo pre"}}
	text := "{% if hooks.pre %}{{ hooks.pre | nl }}{% endif %}{% for file in files %}source \"{{ file }}\"\n{% endfor %}"
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Template("bench", text, data); err != nil {
			b.Fatal(err)
		}
	}
}

package render

// sourceTemplate is the built-in source template, which renders the hooks around the files.
const sourceTemplate = "{{ hooks?.pre | nl }}{% for file in files %}source \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"

// zcompileTemplate is the built-in zsh-only template that keeps each file's compiled form fresh
// while loading it in one pass, mirroring the apply=["defer"] composition model: the guard
// bootstraps and maintains the .zwc (zsh's -ot is false for a missing first operand, so a fresh
// compile is also covered), then source loads the file, which auto-loads a newer .zwc.
const zcompileTemplate = "{{ hooks?.pre | nl }}{% for file in files %}[[ ! -e \"{{ file }}.zwc\" || \"{{ file }}.zwc\" -ot \"{{ file }}\" ]] && zcompile \"{{ file }}\"\nsource \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"

// BuiltinTemplates returns the default templates: `path` and `fpath` are zsh only.
// `zcompile` is zsh-only in effect: under bash it degrades to the plain source template, so
// plugins still load normally (nothing is compiled) and one apply list renders in both shells.
func BuiltinTemplates(shell string) map[string]string {
	templates := map[string]string{
		"source":   sourceTemplate,
		"PATH":     "export PATH=\"{{ dir }}:$PATH\"",
		"zcompile": sourceTemplate,
	}
	if shell == "zsh" {
		templates["path"] = "path=( \"{{ dir }}\" $path )"
		templates["fpath"] = "fpath=( \"{{ dir }}\" $fpath )"
		templates["zcompile"] = zcompileTemplate
	}
	return templates
}

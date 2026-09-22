package render

// sourceTemplate is the built-in source template, which renders the hooks around the files.
const sourceTemplate = "{{ hooks?.pre | nl }}{% for file in files %}source \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"

// zcompileTemplate is the built-in zsh-only template that keeps each file's compiled form fresh.
// zsh's -ot is false when its first operand is missing, so the guard also bootstraps a .zwc.
const zcompileTemplate = "{% for file in files %}[[ ! -e \"{{ file }}.zwc\" || \"{{ file }}.zwc\" -ot \"{{ file }}\" ]] && zcompile \"{{ file }}\"\n{% endfor %}"

// BuiltinTemplates returns the default templates: `path`, `fpath`, and `zcompile` are zsh only.
// `zcompile` is defined for bash as an empty no-op so one apply list renders in both shells.
func BuiltinTemplates(shell string) map[string]string {
	templates := map[string]string{
		"source":   sourceTemplate,
		"PATH":     "export PATH=\"{{ dir }}:$PATH\"",
		"zcompile": "",
	}
	if shell == "zsh" {
		templates["path"] = "path=( \"{{ dir }}\" $path )"
		templates["fpath"] = "fpath=( \"{{ dir }}\" $fpath )"
		templates["zcompile"] = zcompileTemplate
	}
	return templates
}

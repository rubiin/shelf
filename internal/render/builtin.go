package render

// sourceTemplate is the built-in source template, which renders the hooks around the files.
const sourceTemplate = "{{ hooks?.pre | nl }}{% for file in files %}source \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"

// BuiltinTemplates returns the default templates: `path` and `fpath` are zsh only.
func BuiltinTemplates(shell string) map[string]string {
	templates := map[string]string{
		"source": sourceTemplate,
		"PATH":   "export PATH=\"{{ dir }}:$PATH\"",
	}
	if shell == "zsh" {
		templates["path"] = "path=( \"{{ dir }}\" $path )"
		templates["fpath"] = "fpath=( \"{{ dir }}\" $fpath )"
	}
	return templates
}

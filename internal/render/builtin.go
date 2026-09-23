package render

// sourceTemplate is the default: hooks around a source per file.
const sourceTemplate = "{{ hooks?.pre | nl }}{% for file in files %}source \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"

// deferTemplate is the zsh-only defer template: sources queue into the scheduler in
// shelfDeferPreamble while hooks still run immediately.
const deferTemplate = "{{ hooks?.pre | nl }}{% for file in files %}_shelf_defer source \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"

// shelfDeferPreamble is the scheduler deferTemplate queues into: sources run when zle first goes
// idle, after the first prompt. The fd must be top-level (a local fd closes when the precmd hook
// returns), and the guard stops a second `shelf source` redefining the functions or clearing a
// still-pending queue.
const shelfDeferPreamble = `(( ${+functions[_shelf_defer_setup]} )) || {
  _shelf_defer_queue=()
  _shelf_defer() {
    local _shelf_defer_word _shelf_defer_command
    _shelf_defer_command=""
    for _shelf_defer_word in "$@"; do _shelf_defer_command+="${(q)_shelf_defer_word} "; done
    _shelf_defer_queue+=("$_shelf_defer_command")
  }
  _shelf_defer_drain() {
    local _shelf_defer_command
    for _shelf_defer_command in "${_shelf_defer_queue[@]}"; do eval "$_shelf_defer_command"; done
    builtin unset _shelf_defer_queue
  }
  _shelf_defer_idle() {
    local _shelf_defer_command
    zle -F "$_shelf_defer_fd" 2>/dev/null || true
    exec {_shelf_defer_fd}<&-
    builtin unset _shelf_defer_fd
    _shelf_defer_drain
  }
  _shelf_defer_setup() {
    add-zsh-hook -d precmd _shelf_defer_setup
    zle -F "$_shelf_defer_fd" _shelf_defer_idle
  }
  exec {_shelf_defer_fd}<<<""
  autoload -Uz add-zsh-hook
  add-zsh-hook precmd _shelf_defer_setup
}
`

// zcompileTemplate is zsh-only: the guard refreshes the .zwc before sourcing (zsh's -ot is false
// when its first operand is missing, so a first compile is covered), and source then auto-loads
// the newer .zwc.
const zcompileTemplate = "{{ hooks?.pre | nl }}{% for file in files %}[[ ! -e \"{{ file }}.zwc\" || \"{{ file }}.zwc\" -ot \"{{ file }}\" ]] && zcompile \"{{ file }}\"\nsource \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"

// BuiltinTemplates returns the default templates. path and fpath are zsh-only; under bash
// zcompile and defer degrade to plain source, so one apply list works in both shells.
func BuiltinTemplates(shell string) map[string]string {
	templates := map[string]string{
		"source":   sourceTemplate,
		"PATH":     "export PATH=\"{{ dir }}:$PATH\"",
		"zcompile": sourceTemplate,
		"defer":    sourceTemplate,
	}
	if shell == "zsh" {
		templates["path"] = "path=( \"{{ dir }}\" $path )"
		templates["fpath"] = "fpath=( \"{{ dir }}\" $fpath )"
		templates["zcompile"] = zcompileTemplate
		templates["defer"] = deferTemplate
	}
	return templates
}

package render

func BuiltinTemplates(shell string) map[string]string {
	return map[string]string{
		"source": defaultTemplate(shell),
		"PATH":   "export PATH={dir}:$PATH",
		"path":   "path=({dir} $path)",
		"fpath":  "fpath=({dir} $fpath)",
	}
}

package render

func BuiltinTemplates(shell string) map[string]string {
	return map[string]string{
		"source": defaultTemplate(shell),
		"path":   "export PATH={dir}:$PATH",
		"fpath":  "fpath=({dir} $fpath)",
	}
}

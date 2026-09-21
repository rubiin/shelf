package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"shelf/internal/lock"
)

func BenchmarkTmpSourceCommand(b *testing.B) {
	directory := b.TempDir()
	configDir := filepath.Join(directory, "config")
	dataDir := filepath.Join(directory, "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		b.Fatal(err)
	}

	sourceDirectory := filepath.Join(directory, "src")
	var config string
	plugged := lock.LockedConfig{Shell: "zsh", Templates: map[string]string{"source": "{{ hooks?.pre | nl }}{% for file in files %}source \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"}}
	config = "shell = \"zsh\"\n\n"
	for index := range 120 {
		p := filepath.Join(sourceDirectory, fmt.Sprintf("p%d", index))
		if err := os.MkdirAll(p, 0o755); err != nil {
			b.Fatal(err)
		}
		files := make([]string, 0, 2)
		for _, name := range []string{"zsh", "sh"} {
			file := filepath.Join(p, fmt.Sprintf("p%d.%s", index, name))
			if err := os.WriteFile(file, []byte("echo "+name+"\n"), 0o600); err != nil {
				b.Fatal(err)
			}
			files = append(files, file)
		}
		config += fmt.Sprintf("[plugins.p%d]\nlocal = \"%s\"\nuse = [\"p%d.*.zsh\", \"p%d.*.sh\"]\n\n", index, p, index, index)
		plugged.Plugins = append(plugged.Plugins, lock.LockedPlugin{Name: fmt.Sprintf("p%d", index), Directory: p, Files: files, Apply: []string{"source"}})
	}
	configFile := filepath.Join(configDir, "plugins.toml")
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		b.Fatal(err)
	}
	contents, err := os.ReadFile(configFile)
	if err != nil {
		b.Fatal(err)
	}
	plugged.ConfigFingerprint = fingerprintWithShell(contents)
	if err := lock.Write(filepath.Join(dataDir, "plugins.lock"), plugged); err != nil {
		b.Fatal(err)
	}
	b.Setenv("SHELF_CONFIG_DIR", configDir)
	b.Setenv("SHELF_CONFIG_FILE", configFile)
	b.Setenv("SHELF_DATA_DIR", dataDir)

	b.ReportAllocs()
	for b.Loop() {
		if err := Execute([]string{"source"}, io.Discard, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

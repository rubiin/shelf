package lock

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func benchLock(tb testing.TB, plugins int) (string, LockedConfig) {
	tb.Helper()
	directory := tb.TempDir()
	locked := LockedConfig{ConfigFingerprint: "fingerprint", Shell: "zsh", Templates: map[string]string{"source": "{{ hooks?.pre | nl }}{% for file in files %}source \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"}}
	for index := range plugins {
		pluginDirectory := filepath.Join(directory, "p"+strconv.Itoa(index))
		if err := os.MkdirAll(pluginDirectory, 0o755); err != nil {
			tb.Fatal(err)
		}
		files := make([]string, 0, 2)
		for _, name := range []string{"a", "b"} {
			path := filepath.Join(pluginDirectory, name+".zsh")
			if err := os.WriteFile(path, []byte("echo "+name+"\n"), 0o600); err != nil {
				tb.Fatal(err)
			}
			files = append(files, path)
		}
		locked.Plugins = append(locked.Plugins, LockedPlugin{
			Name:      "p" + strconv.Itoa(index),
			Rev:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Directory: pluginDirectory,
			Files:     files,
			Apply:     []string{"source"},
			Hooks:     map[string]string{"pre": "echo pre", "post": "echo post"},
		})
	}
	path := filepath.Join(directory, "plugins.lock")
	if err := Write(path, locked); err != nil {
		tb.Fatal(err)
	}
	return path, locked
}

// The lock file is decoded on every shell start, so its decode cost is the render hot path.
func BenchmarkReadLockFile(b *testing.B) {
	path, _ := benchLock(b, 120)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("lock size: %d bytes", info.Size())
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Read(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerifyLocked(b *testing.B) {
	_, locked := benchLock(b, 120)
	ctx := Context{ConfigFingerprint: "fingerprint", Shell: "zsh"}
	b.ReportAllocs()
	for b.Loop() {
		if !VerifyLocked(locked, ctx) {
			b.Fatal("lock did not verify")
		}
	}
}

package source

import (
	"fmt"
	"os"
	"path/filepath"
)

func installLocal(request Request) (Installed, error) {
	info, err := os.Stat(request.Local)
	if err != nil {
		return Installed{}, fmt.Errorf("local source: %w", err)
	}
	if info.IsDir() {
		return Installed{Directory: filepath.Clean(request.Local)}, nil
	}
	return Installed{Directory: filepath.Dir(request.Local), File: filepath.Clean(request.Local)}, nil
}

func installInline(dataDir string, request Request) (Installed, error) {
	directory := pluginDir(dataDir, request.Name)
	if err := ensureDir(directory); err != nil {
		return Installed{}, err
	}
	file := filepath.Join(directory, "inline.sh")
	if err := os.WriteFile(file, []byte(request.Inline), 0o600); err != nil {
		return Installed{}, err
	}
	return Installed{Directory: directory, File: file}, nil
}

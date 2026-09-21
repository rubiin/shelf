package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func installLocal(request Request) (Installed, error) {
	localPath, err := expandHomePath(request.Local)
	if err != nil {
		return Installed{}, err
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return Installed{}, fmt.Errorf("local source: %w", err)
	}
	if info.IsDir() {
		return Installed{Directory: sourceDirectory(localPath, request.Dir)}, nil
	}
	return Installed{Directory: filepath.Dir(localPath), File: filepath.Clean(localPath)}, nil
}

func expandHomePath(path string) (string, error) {
	path = os.ExpandEnv(path)
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(path, "~/"))), nil
}

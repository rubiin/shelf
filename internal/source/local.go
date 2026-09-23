package source

import (
	"errors"
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
		if request.Optional && errors.Is(err, os.ErrNotExist) {
			return Installed{Skipped: true}, nil
		}
		return Installed{}, fmt.Errorf("local source: %w", err)
	}
	if info.IsDir() {
		sourceDir, err := sourceDirectory(localPath, request.Dir)
		if err != nil {
			return Installed{}, err
		}
		subInfo, err := os.Stat(sourceDir)
		if err != nil {
			return Installed{}, fmt.Errorf("local source dir %q: %w", request.Dir, err)
		}
		if !subInfo.IsDir() {
			return Installed{}, fmt.Errorf("local source dir %q is not a directory", request.Dir)
		}
		return Installed{Directory: sourceDir, Root: filepath.Clean(localPath)}, nil
	}
	return Installed{Directory: filepath.Dir(localPath), Root: filepath.Dir(localPath), File: filepath.Clean(localPath)}, nil
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

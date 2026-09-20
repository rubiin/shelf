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
		return Installed{Directory: sourceDirectory(request.Local, request.Dir)}, nil
	}
	return Installed{Directory: filepath.Dir(request.Local), File: filepath.Clean(request.Local)}, nil
}

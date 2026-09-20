package source

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

func installRemote(ctx context.Context, dataDir string, request Request) (Installed, error) {
	response, err := http.Get(request.Remote)
	if err != nil {
		return Installed{}, fmt.Errorf("download remote source: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Installed{}, fmt.Errorf("download remote source: HTTP %s", response.Status)
	}
	directory := pluginDir(dataDir, request.Name)
	if err := ensureDir(directory); err != nil {
		return Installed{}, err
	}
	file := filepath.Join(directory, remoteFileName(request.Remote))
	temporary, err := os.CreateTemp(directory, ".download-*")
	if err != nil {
		return Installed{}, err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err := temporary.ReadFrom(response.Body); err != nil {
		_ = temporary.Close()
		return Installed{}, err
	}
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return Installed{}, err
	}
	if err := temporary.Close(); err != nil {
		return Installed{}, err
	}
	if err := os.Rename(temporaryName, file); err != nil {
		return Installed{}, err
	}
	return Installed{Directory: directory, File: file}, nil
}

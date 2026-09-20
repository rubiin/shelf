package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
)

// remoteHTTPClient downloads remote sources; a variable so tests can inject a transport.
var remoteHTTPClient = http.DefaultClient

// remoteDrainLimit bounds how much of a failed response body is discarded before reuse.
const remoteDrainLimit = 64 << 10

func installRemote(ctx context.Context, directory, file string, request Request) (Installed, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, request.Remote, nil)
	if err != nil {
		return Installed{}, fmt.Errorf("download remote source: %w", err)
	}
	response, err := remoteHTTPClient.Do(httpRequest)
	if err != nil {
		return Installed{}, fmt.Errorf("download remote source: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, remoteDrainLimit))
		return Installed{}, fmt.Errorf("download remote source: HTTP %s", response.Status)
	}
	if err := ensureDir(directory); err != nil {
		return Installed{}, err
	}
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

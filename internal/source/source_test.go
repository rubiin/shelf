package source

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallerHandlesInlineSource(t *testing.T) {
	installer := NewInstaller(filepath.Join(t.TempDir(), "data"))
	installed, err := installer.Install(context.Background(), Request{Name: "inline", Inline: "echo hello\n"})
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(installed.File)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "echo hello\n" {
		t.Fatalf("inline contents = %q", contents)
	}
}

func TestInstallerDownloadsRemoteSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("echo remote\n"))
	}))
	defer server.Close()

	installer := NewInstaller(filepath.Join(t.TempDir(), "data"))
	installed, err := installer.Install(context.Background(), Request{Name: "remote", Remote: server.URL + "/plugin.zsh"})
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(installed.File)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "echo remote\n" {
		t.Fatalf("remote contents = %q", contents)
	}
}

func TestInstallerRejectsFailedRemoteSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	installer := NewInstaller(filepath.Join(t.TempDir(), "data"))
	_, err := installer.Install(context.Background(), Request{Name: "remote", Remote: server.URL + "/plugin.zsh"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404 Not Found") {
		t.Fatalf("error = %v", err)
	}
}

func TestInstallerStopsRemoteDownloadWhenContextIsCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(5 * time.Second):
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("echo late\n"))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	installer := NewInstaller(filepath.Join(t.TempDir(), "data"))
	_, err := installer.Install(ctx, Request{Name: "remote", Remote: server.URL + "/plugin.zsh"})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

// recordingBody counts how many bytes the installer reads from a failed response body.
type recordingBody struct {
	reader *strings.Reader
	read   int
	eof    bool
}

func (body *recordingBody) Read(buffer []byte) (int, error) {
	count, err := body.reader.Read(buffer)
	body.read += count
	if err == io.EOF {
		body.eof = true
	}
	return count, err
}

func (body *recordingBody) Close() error { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestInstallerPropagatesRequestContext(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "marker")
	var captured context.Context
	previous := remoteHTTPClient
	remoteHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		captured = request.Context()
		return nil, errors.New("stop after capturing context")
	})}
	t.Cleanup(func() { remoteHTTPClient = previous })

	installer := NewInstaller(filepath.Join(t.TempDir(), "data"))
	if _, err := installer.Install(ctx, Request{Name: "remote", Remote: "https://example.com/plugin.zsh"}); err == nil {
		t.Fatal("expected download error")
	}
	if captured == nil || captured.Value(contextKey{}) != "marker" {
		t.Fatalf("request context = %v, want the context passed to Install", captured)
	}
}

func TestInstallerDrainsFailedRemoteResponseBody(t *testing.T) {
	payload := strings.Repeat("x", 4096)
	body := &recordingBody{reader: strings.NewReader(payload)}
	previous := remoteHTTPClient
	remoteHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       body,
			Header:     http.Header{},
		}, nil
	})}
	t.Cleanup(func() { remoteHTTPClient = previous })

	installer := NewInstaller(filepath.Join(t.TempDir(), "data"))
	_, err := installer.Install(context.Background(), Request{Name: "remote", Remote: "https://example.com/plugin.zsh"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404 Not Found") {
		t.Fatalf("error = %v", err)
	}
	if !body.eof {
		t.Fatalf("failed response body was not drained: read %d of %d bytes", body.read, len(payload))
	}
}

func TestInstallerUsesGitSubdirectory(t *testing.T) {
	repository := t.TempDir()
	pluginDirectory := filepath.Join(repository, "plugins", "sudo")
	if err := os.MkdirAll(pluginDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	pluginFile := filepath.Join(pluginDirectory, "sudo.plugin.zsh")
	if err := os.WriteFile(pluginFile, []byte("echo sudo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Shelf Tests"},
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		command := exec.Command("git", args...)
		command.Dir = repository
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}

	installed, err := NewInstaller(filepath.Join(t.TempDir(), "data")).Install(context.Background(), Request{
		Name: "ohmyzsh",
		Git:  repository,
		Dir:  "plugins/sudo",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(installed.Directory)
	if filepath.Base(wantDirectory) != "sudo" || filepath.Base(filepath.Dir(wantDirectory)) != "plugins" {
		t.Fatalf("installed directory = %q", installed.Directory)
	}
	if _, err := os.Stat(filepath.Join(installed.Directory, "sudo.plugin.zsh")); err != nil {
		t.Fatalf("plugin file missing from installed directory: %v", err)
	}
}

func TestGitURLSupportsGistAndProtocols(t *testing.T) {
	tests := []struct {
		name    string
		request Request
		want    string
	}{
		{name: "gist", request: Request{Gist: "579d02802b1cc17baed07753d09f5009"}, want: "https://gist.github.com/579d02802b1cc17baed07753d09f5009.git"},
		{name: "github ssh", request: Request{GitHub: "owner/repository", Protocol: "ssh"}, want: "git@github.com:owner/repository.git"},
		{name: "github git", request: Request{GitHub: "owner/repository", Protocol: "git"}, want: "git://github.com/owner/repository.git"},
		{name: "github https", request: Request{GitHub: "owner/repository", Protocol: "https"}, want: "https://github.com/owner/repository.git"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := gitURL(test.request); got != test.want {
				t.Fatalf("git URL = %q, want %q", got, test.want)
			}
		})
	}
}

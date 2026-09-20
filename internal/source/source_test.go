package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

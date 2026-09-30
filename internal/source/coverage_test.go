package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallLocalSurfacesHomeResolutionError(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := installLocal(Request{Local: "~/plugins"}); err == nil {
		t.Fatal("installLocal accepted a home-relative path without a home directory")
	}
}

func TestExpandHomePathReportsMissingHome(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := expandHomePath("~"); err == nil {
		t.Fatal("expandHomePath resolved ~ without a home directory")
	}
}

func TestInstallGitSurfacesEnsureDirError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only parent directory does not stop root")
	}
	// The clone directory is missing (so it takes the clone branch) but its
	// parent cannot be created, which fails before any git command runs.
	root := t.TempDir()
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	directory := filepath.Join(root, "repos", "repo")
	if _, err := installGit(context.Background(), directory, Request{Git: "https://example.com/repo.git"}); err == nil {
		t.Fatal("installGit accepted a clone path it cannot create")
	}
}

func TestInstallRemoteSurfacesCreateTempError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("payload"))
	}))
	t.Cleanup(server.Close)
	original := remoteHTTPClient
	remoteHTTPClient = server.Client()
	t.Cleanup(func() { remoteHTTPClient = original })

	directory := t.TempDir()
	if err := os.Chmod(directory, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o755) })
	_, err := installRemote(context.Background(), directory, filepath.Join(directory, "out.zsh"), Request{Remote: server.URL})
	if err == nil {
		t.Fatal("installRemote wrote into a read-only directory")
	}
}

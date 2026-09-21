package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func intPtr(value int) *int { return &value }

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

func TestInstallerExpandsLocalHomePath(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	installed, err := NewInstaller(filepath.Join(t.TempDir(), "data")).Install(context.Background(), Request{
		Name:  "foo",
		Local: "~/foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Directory != filepath.Join(home, "foo") {
		t.Fatalf("installed directory = %q, want %q", installed.Directory, filepath.Join(home, "foo"))
	}
}

func TestInstallerExpandsLocalEnvironmentPath(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLUGIN_HOME", home)
	installer := NewInstaller(filepath.Join(t.TempDir(), "data"))

	for _, local := range []string{"$PLUGIN_HOME/foo", "${PLUGIN_HOME}/foo"} {
		installed, err := installer.Install(context.Background(), Request{Name: "foo", Local: local})
		if err != nil {
			t.Fatalf("local path %q: %v", local, err)
		}
		if installed.Directory != filepath.Join(home, "foo") {
			t.Fatalf("local path %q installed directory = %q, want %q", local, installed.Directory, filepath.Join(home, "foo"))
		}
	}
}

func TestInstallerSkipsMissingOptionalLocalSource(t *testing.T) {
	installed, err := NewInstaller(filepath.Join(t.TempDir(), "data")).Install(context.Background(), Request{
		Name:     "optional",
		Local:    filepath.Join(t.TempDir(), "missing"),
		Optional: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !installed.Skipped {
		t.Fatal("missing optional local source was not skipped")
	}
}

func TestInstallerRejectsMissingRequiredLocalSource(t *testing.T) {
	_, err := NewInstaller(filepath.Join(t.TempDir(), "data")).Install(context.Background(), Request{
		Name:  "required",
		Local: filepath.Join(t.TempDir(), "missing"),
	})
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("missing required local source error = %v", err)
	}
}

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

	dataDir := filepath.Join(t.TempDir(), "data")
	installed, err := NewInstaller(dataDir).Install(context.Background(), Request{
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
	// Hostless local repository paths are cloned into the data directory, so the
	// source root is the clone directory GitDirectory names, not the source path.
	wantRoot, err := GitDirectory(dataDir, Request{Git: repository})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Root != wantRoot {
		t.Fatalf("installed root = %q, want %q", installed.Root, wantRoot)
	}
}

func TestLocalFileSourceReportsParentAsRoot(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "plugin.zsh")
	if err := os.WriteFile(localPath, []byte("echo hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installed, err := NewInstaller(filepath.Join(t.TempDir(), "data")).Install(context.Background(), Request{Name: "demo", Local: localPath})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Root != filepath.Dir(localPath) {
		t.Fatalf("installed root = %q, want %q", installed.Root, filepath.Dir(localPath))
	}
}

func TestGitDirectoryLayout(t *testing.T) {
	dataDir := t.TempDir()
	tests := []struct {
		name    string
		request Request
		want    string
	}{
		{
			name:    "github",
			request: Request{GitHub: "rubiin/plugin-test"},
			want:    filepath.Join(dataDir, "repos", "github.com", "rubiin", "plugin-test"),
		},
		{
			name:    "gist",
			request: Request{Gist: "rubiin/579d02802b1cc17baed07753d09f5009"},
			want:    filepath.Join(dataDir, "repos", "gist.github.com", "rubiin", "579d02802b1cc17baed07753d09f5009"),
		},
		{
			name:    "gitlab",
			request: Request{GitLab: "owner/repository"},
			want:    filepath.Join(dataDir, "repos", "gitlab.com", "owner", "repository"),
		},
		{
			name:    "bitbucket",
			request: Request{Bitbucket: "team/project"},
			want:    filepath.Join(dataDir, "repos", "bitbucket.org", "team", "project"),
		},
		{
			name:    "codeberg",
			request: Request{Codeberg: "owner/repository"},
			want:    filepath.Join(dataDir, "repos", "codeberg.org", "owner", "repository"),
		},
		{
			name:    "git url keeps its suffix",
			request: Request{Git: "https://example.com/plugins/plugin.git"},
			want:    filepath.Join(dataDir, "repos", "example.com", "plugins", "plugin.git"),
		},
		{
			name:    "local repository path",
			request: Request{Git: "/srv/repositories/demo"},
			want:    filepath.Join(dataDir, "repos", "srv", "repositories", "demo"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := GitDirectory(dataDir, test.request)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("git directory = %q, want %q", got, test.want)
			}
		})
	}
	if _, err := GitDirectory(dataDir, Request{Git: "https://example.com"}); err == nil {
		t.Fatal("a host-only URL was accepted")
	}
}

// TestCloneURLMultiForgeProtocols verifies each forge shorthand builds the expected
// clone URL, and that proto selects the scheme prefix for all of them.
func TestCloneURLMultiForgeProtocols(t *testing.T) {
	tests := []struct {
		request Request
		want    string
	}{
		{request: Request{GitHub: "owner/repo"}, want: "https://github.com/owner/repo"},
		{request: Request{GitHub: "owner/repo", Proto: "ssh"}, want: "ssh://git@github.com/owner/repo"},
		{request: Request{Gist: "abc123", Proto: "git"}, want: "git://gist.github.com/abc123"},
		{request: Request{GitLab: "owner/repo"}, want: "https://gitlab.com/owner/repo"},
		{request: Request{GitLab: "owner/repo", Proto: "ssh"}, want: "ssh://git@gitlab.com/owner/repo"},
		{request: Request{Bitbucket: "team/project"}, want: "https://bitbucket.org/team/project"},
		{request: Request{Bitbucket: "team/project", Proto: "git"}, want: "git://bitbucket.org/team/project"},
		{request: Request{Codeberg: "owner/repo", Proto: "ssh"}, want: "ssh://git@codeberg.org/owner/repo"},
	}
	for _, test := range tests {
		if got := CloneURL(test.request); got != test.want {
			t.Fatalf("CloneURL(%+v) = %q, want %q", test.request, got, test.want)
		}
	}
}

func TestRemoteDirectoryLayout(t *testing.T) {
	dataDir := t.TempDir()
	directory, file, err := RemoteDirectory(dataDir, "https://github.com/rubiin/dotfiles/raw/0.3.0/LICENSE-MIT")
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(dataDir, "downloads", "github.com", "rubiin", "dotfiles", "raw", "0.3.0")
	if directory != wantDirectory {
		t.Fatalf("download directory = %q, want %q", directory, wantDirectory)
	}
	if want := filepath.Join(wantDirectory, "LICENSE-MIT"); file != want {
		t.Fatalf("download file = %q, want %q", file, want)
	}

	rootDirectory, rootFile, err := RemoteDirectory(dataDir, "https://example.com/plugin.zsh")
	if err != nil {
		t.Fatal(err)
	}
	if rootDirectory != filepath.Join(dataDir, "downloads", "example.com") {
		t.Fatalf("root download directory = %q", rootDirectory)
	}
	if rootFile != filepath.Join(rootDirectory, "plugin.zsh") {
		t.Fatalf("root download file = %q", rootFile)
	}
	if _, _, err := RemoteDirectory(dataDir, "/plugins/plugin.zsh"); err == nil {
		t.Fatal("a hostless remote URL was accepted")
	}
}

func TestInstallerClonesIntoTheSourceLayout(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "plugin.zsh"), []byte("echo test\n"), 0o600); err != nil {
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

	dataDir := filepath.Join(t.TempDir(), "data")
	installed, err := NewInstaller(dataDir).Install(context.Background(), Request{Name: "demo", Git: repository})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(installed.Directory, CloneDir(dataDir)+string(filepath.Separator)) {
		t.Fatalf("installed directory = %q, want a path under %q", installed.Directory, CloneDir(dataDir))
	}
	if filepath.Base(installed.Directory) != filepath.Base(repository) {
		t.Fatalf("installed directory = %q, want it to end with %q", installed.Directory, filepath.Base(repository))
	}
	if _, err := os.Stat(filepath.Join(installed.Directory, ".git")); err != nil {
		t.Fatalf("clone is missing: %v", err)
	}
}

func TestInstallerDownloadsIntoTheSourceLayout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("echo remote\n"))
	}))
	defer server.Close()

	dataDir := filepath.Join(t.TempDir(), "data")
	installed, err := NewInstaller(dataDir).Install(context.Background(), Request{Name: "remote", Remote: server.URL + "/plugins/plugin.zsh"})
	if err != nil {
		t.Fatal(err)
	}
	// Keys downloads on the URL host without the port, like url.host_str().
	host, _, _ := strings.Cut(strings.TrimPrefix(server.URL, "http://"), ":")
	want := filepath.Join(DownloadDir(dataDir), host, "plugins", "plugin.zsh")
	if installed.File != want {
		t.Fatalf("downloaded file = %q, want %q", installed.File, want)
	}
	if installed.Directory != filepath.Dir(want) {
		t.Fatalf("download directory = %q, want %q", installed.Directory, filepath.Dir(want))
	}
	contents, err := os.ReadFile(installed.File)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "echo remote\n" {
		t.Fatalf("remote contents = %q", contents)
	}
}

// TestInstallerShallowClonesGitSources verifies that fresh installs fetch only the
// requested ref's tip (or the remote default branch), so source repositories with long
// histories aren't transferred wholesale. file:// URLs exercise the real shallow
// transport: git ignores --depth for plain local-path clones.
func TestInstallerShallowClonesGitSources(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	first := commitFile(t, repository, "plugin.zsh", "echo first\n")
	tip := commitFile(t, repository, "plugin.zsh", "echo second\n")
	if first == tip {
		t.Fatal("test setup: expected two distinct commits")
	}
	if output, err := exec.Command("git", "-C", repository, "branch", "featured").CombinedOutput(); err != nil {
		t.Fatalf("git branch featured: %v\n%s", err, output)
	}
	if output, err := exec.Command("git", "-C", repository, "tag", "v1", "-m", "v1").CombinedOutput(); err != nil {
		t.Fatalf("git tag v1: %v\n%s", err, output)
	}
	sourceURL := "file://" + repository

	tests := []struct {
		name    string
		request Request
	}{
		{name: "default branch", request: Request{Name: "default", Git: sourceURL}},
		{name: "branch", request: Request{Name: "branch", Git: sourceURL, Branch: "featured"}},
		{name: "tag", request: Request{Name: "tag", Git: sourceURL, Tag: "v1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "data")
			installed, err := NewInstaller(dataDir).Install(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if installed.Revision != tip {
				t.Fatalf("revision = %q, want %q", installed.Revision, tip)
			}
			if _, err := os.Stat(filepath.Join(installed.Directory, ".git", "shallow")); err != nil {
				t.Fatalf("fresh install was not a shallow clone: %v", err)
			}
		})
	}
}

// TestInstallerHonorsPerPluginDepth verifies depth reaches git clone: unset keeps
// the shallow depth-1 default, an explicit depth fetches that many ancestors, and
// depth 0 clones full history (like zplug's depth:0).
func TestInstallerHonorsPerPluginDepth(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := range 3 {
		commitFile(t, repository, "plugin.zsh", fmt.Sprintf("echo commit-%d\n", index))
	}
	sourceURL := "file://" + repository

	tests := []struct {
		name        string
		depth       *int
		wantCommits int
		wantShallow bool
	}{
		{name: "unset keeps shallow default", wantCommits: 1, wantShallow: true},
		{name: "depth 2 keeps two ancestors", depth: intPtr(2), wantCommits: 2, wantShallow: true},
		{name: "depth 0 clones full history", depth: intPtr(0), wantCommits: 3, wantShallow: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "data")
			installed, err := NewInstaller(dataDir).Install(context.Background(), Request{Name: "demo", Git: sourceURL, Depth: test.depth})
			if err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command("git", "-C", installed.Directory, "rev-list", "--count", "HEAD").Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(output)); got != strconv.Itoa(test.wantCommits) {
				t.Fatalf("reachable commits = %s, want %d", got, test.wantCommits)
			}
			_, statErr := os.Stat(filepath.Join(installed.Directory, ".git", "shallow"))
			if test.wantShallow && os.IsNotExist(statErr) {
				t.Fatal("expected a shallow clone marker")
			}
			if !test.wantShallow && statErr == nil {
				t.Fatal("expected a full clone, found a shallow marker")
			}
		})
	}
}

// TestInstallerPassesCloneOptionsThrough verifies cloneopts reach git clone: a
// --no-tags clone must have no tags while the default clone fetches them.
func TestInstallerPassesCloneOptionsThrough(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repository, "plugin.zsh", "echo first\n")
	commitFile(t, repository, "plugin.zsh", "echo second\n")
	if output, err := exec.Command("git", "-C", repository, "tag", "v1", "-m", "v1").CombinedOutput(); err != nil {
		t.Fatalf("git tag v1: %v\n%s", err, output)
	}
	sourceURL := "file://" + repository

	cloneTags := func(t *testing.T, request Request) string {
		t.Helper()
		installed, err := NewInstaller(filepath.Join(t.TempDir(), "data")).Install(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("git", "-C", installed.Directory, "tag").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(output))
	}

	if tags := cloneTags(t, Request{Name: "default", Git: sourceURL}); tags == "" {
		t.Fatal("default clone fetched no tags")
	}
	if tags := cloneTags(t, Request{Name: "notags", Git: sourceURL, CloneOpts: []string{"--no-tags"}}); tags != "" {
		t.Fatalf("clone with --no-tags fetched tags %q", tags)
	}
}

func TestInstallerFallsBackToFullCloneForPinnedRevision(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	pinned := commitFile(t, repository, "plugin.zsh", "echo first\n")
	_ = commitFile(t, repository, "plugin.zsh", "echo second\n")

	dataDir := filepath.Join(t.TempDir(), "data")
	installed, err := NewInstaller(dataDir).Install(context.Background(), Request{
		Name: "pinned",
		Git:  "file://" + repository,
		Ref:  pinned,
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Revision != pinned {
		t.Fatalf("revision = %q, want the pinned commit %q", installed.Revision, pinned)
	}
	if _, err := os.Stat(filepath.Join(installed.Directory, ".git", "shallow")); !os.IsNotExist(err) {
		t.Fatalf("fallback installed revision %q through a shallow clone; want a full clone", pinned)
	}
}

// commitFile commits contents to a file in the repository directory and returns the
// resulting revision. It seeds a git repository on first use, like the CLI e2e helper.
func commitFile(t *testing.T, directory, file, contents string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(directory, ".git")); os.IsNotExist(err) {
		for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Shelf Tests"}} {
			if output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, output)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(directory, file), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", file}, {"commit", "-m", contents}} {
		if output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	output, err := exec.Command("git", "-C", directory, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func TestGitURLProtocolPrefixes(t *testing.T) {
	tests := []struct {
		name    string
		request Request
		want    string
	}{
		{name: "gist", request: Request{Gist: "579d02802b1cc17baed07753d09f5009"}, want: "https://gist.github.com/579d02802b1cc17baed07753d09f5009"},
		{name: "github ssh", request: Request{GitHub: "rubiin/repository", Proto: "ssh"}, want: "ssh://git@github.com/rubiin/repository"},
		{name: "github git", request: Request{GitHub: "rubiin/repository", Proto: "git"}, want: "git://github.com/rubiin/repository"},
		{name: "github https", request: Request{GitHub: "rubiin/repository", Proto: "https"}, want: "https://github.com/rubiin/repository"},
		{name: "git url unchanged", request: Request{Git: "https://example.com/plugin.git"}, want: "https://example.com/plugin.git"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := gitURL(test.request); got != test.want {
				t.Fatalf("git URL = %q, want %q", got, test.want)
			}
		})
	}
}

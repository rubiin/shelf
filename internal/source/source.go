package source

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Request struct {
	Name      string
	Git       string
	GitHub    string
	Gist      string
	GitLab    string
	Bitbucket string
	Codeberg  string
	Proto     string
	Remote    string
	Local     string
	Optional  bool
	Ref       string
	Branch    string
	Tag       string
	Dir       string
	File      string
	Update    bool
	Reinstall bool
	// CloneOpts are extra arguments passed to git clone; Depth sets --depth (nil keeps the shallow default, 0 clones full history).
	CloneOpts []string
	Depth     *int
}

// gitURL builds the clone URL the way from `proto`: a scheme prefix, then host/repository.
func gitURL(request Request) string {
	if request.Git != "" {
		return request.Git
	}
	repository := request.GitHub
	host := "github.com"
	switch {
	case request.Gist != "":
		repository = request.Gist
		host = "gist.github.com"
	case request.GitLab != "":
		repository = request.GitLab
		host = "gitlab.com"
	case request.Bitbucket != "":
		repository = request.Bitbucket
		host = "bitbucket.org"
	case request.Codeberg != "":
		repository = request.Codeberg
		host = "codeberg.org"
	}
	prefix := "https://"
	switch request.Proto {
	case "git":
		prefix = "git://"
	case "ssh":
		prefix = "ssh://git@"
	}
	return prefix + host + "/" + repository
}

type Installed struct {
	Directory string
	// Root is the plugin's source root before dir narrowing; build hooks run here.
	Root     string
	File     string
	Revision string
	Skipped  bool
}

type Installer interface {
	Install(context.Context, Request) (Installed, error)
}

type installer struct{ dataDir string }

func NewInstaller(dataDir string) Installer { return installer{dataDir: dataDir} }

// CloneURL resolves the URL a git source clones from, so a lock can record it.
func CloneURL(request Request) string { return gitURL(request) }

// installLocks serializes installs into one directory, which two plugins sharing a source can target.
var installLocks sync.Map

func lockInstall(directory string) func() {
	value, _ := installLocks.LoadOrStore(directory, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func (i installer) Install(ctx context.Context, request Request) (Installed, error) {
	if request.Name == "" {
		return Installed{}, fmt.Errorf("source name is empty")
	}
	switch {
	case request.Remote != "":
		directory, file, err := RemoteDirectory(i.dataDir, request.Remote)
		if err != nil {
			return Installed{}, err
		}
		defer lockInstall(directory)()
		return installRemote(ctx, directory, file, request)
	case request.Local != "":
		return installLocal(request)
	default:
		directory, err := GitDirectory(i.dataDir, request)
		if err != nil {
			return Installed{}, err
		}
		defer lockInstall(directory)()
		return installGit(ctx, directory, request)
	}
}

// CloneDir is the directory git sources are cloned into.
func CloneDir(dataDir string) string { return filepath.Join(dataDir, "repos") }

// DownloadDir is the directory remote sources are downloaded into.
func DownloadDir(dataDir string) string { return filepath.Join(dataDir, "downloads") }

// GitDirectory is a git source's clone directory: <clone dir>/<host>/<repo path>; a hostless source keeps its path below the clone directory.
func GitDirectory(dataDir string, request Request) (string, error) {
	rawURL := gitURL(request)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("git source %q is not a URL: %w", rawURL, err)
	}
	relative := strings.Trim(parsed.Path, "/")
	if relative == "" {
		return "", fmt.Errorf("git source %q has no repository path", rawURL)
	}
	segments := []string{CloneDir(dataDir)}
	if host := parsed.Hostname(); host != "" {
		segments = append(segments, host)
	}
	return filepath.Join(append(segments, filepath.FromSlash(relative))...), nil
}

// RemoteDirectory is a remote source's download directory and file: <download dir>/<host>/<path>.
func RemoteDirectory(dataDir, rawURL string) (string, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("remote source %q is not a URL: %w", rawURL, err)
	}
	if parsed.Hostname() == "" {
		return "", "", fmt.Errorf("remote source %q has no host", rawURL)
	}
	var segments []string
	for _, segment := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		segments = []string{"index"}
	}
	parents := append([]string{DownloadDir(dataDir), parsed.Hostname()}, segments[:len(segments)-1]...)
	directory := filepath.Join(parents...)
	return directory, filepath.Join(directory, segments[len(segments)-1]), nil
}

func ensureDir(path string) error { return os.MkdirAll(path, 0o755) }

// sourceDirectory resolves a plugin's dir narrowing below its source root, rejecting any path that escapes it.
func sourceDirectory(root, directory string) (string, error) {
	if directory == "" || directory == "." {
		return filepath.Clean(root), nil
	}
	joined := filepath.Join(root, filepath.Clean(directory))
	relative, err := filepath.Rel(root, joined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dir %q escapes the plugin source directory", directory)
	}
	return joined, nil
}

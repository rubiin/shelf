package source

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

type Request struct {
	Name      string
	Git       string
	GitHub    string
	Gist      string
	Protocol  string
	Remote    string
	Local     string
	Inline    string
	Ref       string
	Branch    string
	Tag       string
	Dir       string
	File      string
	Update    bool
	Reinstall bool
}

func gitURL(request Request) string {
	if request.Git != "" {
		return request.Git
	}
	repository := request.GitHub
	host := "github.com"
	if request.Gist != "" {
		repository = request.Gist
		host = "gist.github.com"
	}
	switch request.Protocol {
	case "ssh":
		return "git@" + host + ":" + repository + ".git"
	case "git":
		return "git://" + host + "/" + repository + ".git"
	default:
		return "https://" + host + "/" + repository + ".git"
	}
}

type Installed struct {
	Directory string
	File      string
}

type Installer interface {
	Install(context.Context, Request) (Installed, error)
}

type installer struct{ dataDir string }

func NewInstaller(dataDir string) Installer { return installer{dataDir: dataDir} }

func (i installer) Install(ctx context.Context, request Request) (Installed, error) {
	if request.Name == "" {
		return Installed{}, fmt.Errorf("source name is empty")
	}
	if request.Inline != "" {
		return installInline(i.dataDir, request)
	}
	if request.Remote != "" {
		return installRemote(ctx, i.dataDir, request)
	}
	if request.Local != "" {
		return installLocal(request)
	}
	return installGit(ctx, i.dataDir, request)
}

func pluginDir(dataDir, name string) string { return filepath.Join(dataDir, "plugins", name) }

func remoteFileName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err == nil && filepath.Base(parsed.Path) != "." && filepath.Base(parsed.Path) != "/" {
		return filepath.Base(parsed.Path)
	}
	return "plugin"
}

func ensureDir(path string) error { return os.MkdirAll(path, 0o755) }

func sourceDirectory(root, directory string) string {
	if directory == "" || directory == "." {
		return filepath.Clean(root)
	}
	return filepath.Join(root, filepath.Clean(directory))
}

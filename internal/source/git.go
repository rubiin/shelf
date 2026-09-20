package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func installGit(ctx context.Context, dataDir string, request Request) (Installed, error) {
	url := request.Git
	if url == "" {
		url = "https://github.com/" + request.GitHub + ".git"
	}
	if url == "https://github.com/.git" {
		return Installed{}, fmt.Errorf("git source is empty")
	}
	directory := pluginDir(dataDir, request.Name)
	if request.Reinstall {
		if err := os.RemoveAll(directory); err != nil {
			return Installed{}, err
		}
	}
	if _, err := os.Stat(filepath.Join(directory, ".git")); os.IsNotExist(err) {
		if err := ensureDir(filepath.Dir(directory)); err != nil {
			return Installed{}, err
		}
		args := []string{"clone", "--", url, directory}
		if err := runGit(ctx, args...); err != nil {
			return Installed{}, err
		}
	} else if request.Update {
		if err := runGitIn(ctx, directory, "fetch", "--all", "--tags"); err != nil {
			return Installed{}, err
		}
	}
	ref := request.Ref
	if ref == "" {
		ref = request.Branch
	}
	if ref == "" {
		ref = request.Tag
	}
	if ref != "" {
		if err := runGitIn(ctx, directory, "checkout", "--detach", ref); err != nil {
			return Installed{}, err
		}
	}
	return Installed{Directory: sourceDirectory(directory, request.Dir)}, nil
}

func runGit(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, "git", args...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, output)
	}
	return nil
}

func runGitIn(ctx context.Context, directory string, args ...string) error {
	commandArgs := append([]string{"-C", directory}, args...)
	return runGit(ctx, commandArgs...)
}

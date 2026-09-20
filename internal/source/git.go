package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func installGit(ctx context.Context, directory string, request Request) (Installed, error) {
	repositoryURL := gitURL(request)
	if request.Git == "" && request.GitHub == "" && request.Gist == "" {
		return Installed{}, fmt.Errorf("git source is empty")
	}
	if request.Reinstall {
		if err := os.RemoveAll(directory); err != nil {
			return Installed{}, err
		}
	}
	if _, err := os.Stat(filepath.Join(directory, ".git")); os.IsNotExist(err) {
		if err := ensureDir(filepath.Dir(directory)); err != nil {
			return Installed{}, err
		}
		args := []string{"clone", "--recurse-submodules", "--", repositoryURL, directory}
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
	revision, err := gitOutputIn(ctx, directory, "rev-parse", "HEAD")
	if err != nil {
		return Installed{}, err
	}
	return Installed{Directory: sourceDirectory(directory, request.Dir), Revision: strings.TrimSpace(revision)}, nil
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

func gitOutputIn(ctx context.Context, directory string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %v: %w", args, err)
	}
	return string(output), nil
}

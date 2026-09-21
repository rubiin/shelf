package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func installGit(ctx context.Context, directory string, request Request) (Installed, error) {
	repositoryURL := gitURL(request)
	if request.Git == "" && request.GitHub == "" && request.Gist == "" {
		return Installed{}, fmt.Errorf("git source is empty")
	}
	ref := request.Ref
	if ref == "" {
		ref = request.Branch
	}
	if ref == "" {
		ref = request.Tag
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
		// Fresh installs shallow-clone the requested ref's tip; a depth-limited clone may not reach a pinned bare SHA, so fall back to a full clone. CloneOpts pass through, and depth 0 is a full clone.
		args := []string{"clone"}
		args = append(args, request.CloneOpts...)
		if request.Depth == nil {
			args = append(args, "--depth", "1")
		} else if *request.Depth > 0 {
			args = append(args, "--depth", strconv.Itoa(*request.Depth))
		}
		args = append(args, "--recurse-submodules")
		if ref != "" {
			args = append(args, "--branch", ref)
		}
		args = append(args, "--", repositoryURL, directory)
		if err := runGit(ctx, args...); err != nil {
			if err := os.RemoveAll(directory); err != nil {
				return Installed{}, err
			}
			args := []string{"clone"}
			args = append(args, request.CloneOpts...)
			args = append(args, "--recurse-submodules", "--", repositoryURL, directory)
			if err := runGit(ctx, args...); err != nil {
				return Installed{}, err
			}
		}
	} else if request.Update {
		if err := runGitIn(ctx, directory, "fetch", "--all", "--tags"); err != nil {
			return Installed{}, err
		}
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
	return Installed{Directory: sourceDirectory(directory, request.Dir), Root: directory, Revision: strings.TrimSpace(revision)}, nil
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

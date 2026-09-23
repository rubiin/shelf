package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func installGit(ctx context.Context, directory string, request Request) (Installed, error) {
	repositoryURL := gitURL(request)
	if !hasGitSource(request) {
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
	gitDir := filepath.Join(directory, ".git")
	_, statErr := os.Stat(gitDir)
	fetched := false
	switch {
	case statErr == nil:
		if request.Update && !request.Frozen {
			if err := runGitIn(ctx, directory, "fetch", "--all", "--tags"); err != nil {
				return Installed{}, err
			}
			fetched = true
		}
	case errors.Is(statErr, os.ErrNotExist):
		if err := ensureDir(filepath.Dir(directory)); err != nil {
			return Installed{}, err
		}
		// Shallow by default; a pinned SHA can sit beyond the depth, so a failed clone retries full.
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
	default:
		return Installed{}, fmt.Errorf("check existing clone at %s: %w", gitDir, statErr)
	}
	if ref != "" {
		// fetch only moves refs/remotes/origin/*; a branch ref must follow that tip,
		// not the local branch the clone created, or an update keeps the old commit.
		target := ref
		if remoteBranch := "refs/remotes/origin/" + ref; gitHasRef(ctx, directory, remoteBranch) {
			target = remoteBranch
		} else if err := runGitIn(ctx, directory, "cat-file", "-e", ref+"^{commit}"); err != nil {
			// A shallow clone may not hold the pinned revision; fetch the object first.
			if err := runGitIn(ctx, directory, "fetch", "--depth", "1", "origin", ref); err != nil {
				return Installed{}, err
			}
		}
		if err := runGitIn(ctx, directory, "checkout", "--detach", target); err != nil {
			return Installed{}, err
		}
	} else if fetched && gitHasRef(ctx, directory, "refs/remotes/origin/HEAD") {
		// An unpinned update tracks the remote default branch.
		if err := runGitIn(ctx, directory, "checkout", "--detach", "refs/remotes/origin/HEAD"); err != nil {
			return Installed{}, err
		}
	}
	revision, err := gitOutputIn(ctx, directory, "rev-parse", "HEAD")
	if err != nil {
		return Installed{}, err
	}
	sourceDir, err := sourceDirectory(directory, request.Dir)
	if err != nil {
		return Installed{}, err
	}
	return Installed{Directory: sourceDir, Root: directory, Revision: strings.TrimSpace(revision)}, nil
}

// CheckedOutRevision reads the clone's HEAD without writing to the repository, so it
// is safe under a shared lock.
func CheckedOutRevision(ctx context.Context, dataDir string, request Request) (string, error) {
	directory, err := GitDirectory(dataDir, request)
	if err != nil {
		return "", err
	}
	revision, err := gitOutputIn(ctx, directory, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(revision), nil
}

func hasGitSource(request Request) bool {
	return request.Git != "" || request.GitHub != "" || request.Gist != "" || request.GitLab != "" || request.Bitbucket != "" || request.Codeberg != ""
}

func gitHasRef(ctx context.Context, directory, ref string) bool {
	return runGitIn(ctx, directory, "rev-parse", "--verify", "--quiet", ref+"^{commit}") == nil
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

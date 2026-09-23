package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Shell string

const (
	Bash Shell = "bash"
	Zsh  Shell = "zsh"
)

func DefaultConfig(shell Shell) string {
	return "shell = \"" + string(shell) + "\"\n\n[plugins]\n"
}

func Initialize(path string, shell Shell) error {
	if path == "" {
		return errors.New("config path is empty")
	}
	if shell != Bash && shell != Zsh {
		return errors.New("unsupported shell: " + string(shell))
	}
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return fmt.Errorf("config path %q is a directory", path)
	case err == nil:
		// Existing file: already initialized, leave it untouched.
		return nil
	case !os.IsNotExist(err):
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Write via temp file + rename so a partial write never lands at path.
	return writeAtomically(path, []byte(DefaultConfig(shell)))
}

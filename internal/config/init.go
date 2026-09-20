package config

import (
	"errors"
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
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(DefaultConfig(shell)), 0o600)
}

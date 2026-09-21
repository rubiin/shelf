// Package filelock serializes shelf processes through an advisory flock on the config directory.
package filelock

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// Guard is an advisory lock on a directory, released by Release or when the process exits.
type Guard struct {
	file *os.File
}

// Acquire locks the directory itself with flock, blocking while another process
// holds it; a missing directory is left unlocked. The directory is opened
// read-only and locked directly, so no lock file is created inside it.
func Acquire(directory string, exclusive bool, warnings io.Writer) (*Guard, error) {
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	file, err := os.Open(directory)
	if err != nil {
		return nil, err
	}
	operation := syscall.LOCK_SH
	if exclusive {
		operation = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(file.Fd()), operation|syscall.LOCK_NB); err != nil {
		if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = file.Close()
			return nil, err
		}
		_, _ = fmt.Fprintf(warnings, "   Blocking waiting for file lock on %s\n", directory)
		if err := syscall.Flock(int(file.Fd()), operation); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	return &Guard{file: file}, nil
}

// Release unlocks the guard; a nil guard means the directory was not locked.
func (guard *Guard) Release() error {
	if guard == nil {
		return nil
	}
	defer func() { _ = guard.file.Close() }()
	return syscall.Flock(int(guard.file.Fd()), syscall.LOCK_UN)
}

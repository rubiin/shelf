// Package filelock serializes shelf processes through an advisory flock on the config directory.
package filelock

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
)

// Guard is an advisory lock on a directory, released by Release or when the process exits.
type Guard struct {
	file         *os.File
	releasedOnce sync.Once
	releaseError error
}

// Acquire blocks while another process holds the lock; a missing directory returns
// nil, nil and no lock file is created.
func Acquire(directory string, exclusive bool, warnings io.Writer) (*Guard, error) {
	file, err := os.Open(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.IsDir() {
		_ = file.Close()
		return nil, fmt.Errorf("%s is not a directory", directory)
	}
	operation := syscall.LOCK_SH
	if exclusive {
		operation = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(file.Fd()), operation|syscall.LOCK_NB); err != nil {
		// EAGAIN/EWOULDBLOCK mean the lock is held, EINTR means interrupted; retry blocking.
		if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = file.Close()
			return nil, err
		}
		if warnings != nil {
			_, _ = fmt.Fprintf(warnings, "   Blocking waiting for file lock on %s\n", directory)
		}
		for {
			err = syscall.Flock(int(file.Fd()), operation)
			if err == nil {
				break
			}
			if !errors.Is(err, syscall.EINTR) {
				_ = file.Close()
				return nil, err
			}
		}
	}
	return &Guard{file: file}, nil
}

// Release is safe on a nil guard or when called twice; the first result wins.
func (guard *Guard) Release() error {
	if guard == nil {
		return nil
	}
	guard.releasedOnce.Do(func() {
		defer func() { _ = guard.file.Close() }()
		guard.releaseError = syscall.Flock(int(guard.file.Fd()), syscall.LOCK_UN)
	})
	return guard.releaseError
}

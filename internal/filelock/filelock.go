// Package filelock serializes shelf processes through an advisory flock on the config directory.
package filelock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// Guard is an advisory lock on a directory, released by Release or when the process exits.
type Guard struct {
	file         *os.File
	releasedOnce sync.Once
	releaseError error
}

// lockRetryInterval paces the non-blocking flock retries while waiting for a held lock.
const lockRetryInterval = 25 * time.Millisecond

// Acquire blocks while another process holds the lock; a missing directory returns
// nil, nil and no lock file is created.
func Acquire(directory string, exclusive bool, warnings io.Writer) (*Guard, error) {
	return acquire(directory, exclusive, warnings, nil)
}

// AcquireContext is Acquire with timeout or cancellation: while another process holds
// the lock it retries until ctx is done, then returns ctx.Err() instead of blocking
// forever.
func AcquireContext(ctx context.Context, directory string, exclusive bool, warnings io.Writer) (*Guard, error) {
	return acquire(directory, exclusive, warnings, ctx)
}

func acquire(directory string, exclusive bool, warnings io.Writer, ctx context.Context) (*Guard, error) {
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
	// A caller whose deadline already passed must fail fast, not take the lock.
	if ctx != nil {
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, contextError(directory, ctx)
		default:
		}
	}
	if err := syscall.Flock(int(file.Fd()), operation|syscall.LOCK_NB); err != nil {
		// EAGAIN/EWOULDBLOCK mean the lock is held, EINTR means interrupted; retry.
		if !isRetryable(err) {
			_ = file.Close()
			return nil, err
		}
		if warnings != nil {
			_, _ = fmt.Fprintf(warnings, "   Blocking waiting for file lock on %s\n", directory)
		}
		if ctx == nil {
			return waitBlocking(file, operation)
		}
		return waitContext(ctx, file, operation, directory)
	}
	return &Guard{file: file}, nil
}

// waitBlocking retries the flock until granted; EINTR restarts the wait.
func waitBlocking(file *os.File, operation int) (*Guard, error) {
	for {
		if err := syscall.Flock(int(file.Fd()), operation); err == nil {
			return &Guard{file: file}, nil
		} else if !errors.Is(err, syscall.EINTR) {
			_ = file.Close()
			return nil, err
		}
	}
}

// waitContext retries the non-blocking flock so a cancelled or expired context can end
// the wait; a blocking flock call itself cannot be interrupted.
func waitContext(ctx context.Context, file *os.File, operation int, directory string) (*Guard, error) {
	ticker := time.NewTicker(lockRetryInterval)
	defer ticker.Stop()
	for {
		if err := syscall.Flock(int(file.Fd()), operation|syscall.LOCK_NB); err == nil {
			return &Guard{file: file}, nil
		} else if !isRetryable(err) {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, contextError(directory, ctx)
		case <-ticker.C:
		}
	}
}

func contextError(directory string, ctx context.Context) error {
	return fmt.Errorf("acquire file lock on %s: %w", directory, ctx.Err())
}

func isRetryable(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EINTR)
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

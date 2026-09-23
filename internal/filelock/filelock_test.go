package filelock

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestAcquireBlocksASecondExclusiveLock(t *testing.T) {
	directory := t.TempDir()
	first, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("exclusive lock was skipped for an existing directory")
	}

	acquired := make(chan struct{})
	go func() {
		guard, err := Acquire(directory, true, io.Discard)
		if err == nil {
			_ = guard.Release()
		}
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("a second exclusive lock was granted while the first was held")
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("the second exclusive lock was not granted after release")
	}
}

func TestAcquireSharesConcurrentReadLocks(t *testing.T) {
	directory := t.TempDir()
	first, err := Acquire(directory, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()
	second, err := Acquire(directory, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Release() }()
}

func TestAcquireCreatesNoLockFile(t *testing.T) {
	directory := t.TempDir()
	before, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("lock acquisition changed directory contents: before %d entries, after %d", len(before), len(after))
	}
}

func TestAcquireReportsWhenItWaits(t *testing.T) {
	directory := t.TempDir()
	first, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()

	var warnings bytes.Buffer
	acquired := make(chan *Guard)
	go func() {
		guard, err := Acquire(directory, true, &warnings)
		if err != nil {
			t.Error(err)
		}
		acquired <- guard
	}()
	time.Sleep(50 * time.Millisecond)
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	guard := <-acquired
	defer func() { _ = guard.Release() }()
	if !bytes.Contains(warnings.Bytes(), []byte("file lock")) {
		t.Fatalf("warnings = %q, want a waiting message", warnings.String())
	}
}

// TestAcquireContextSucceedsWhenFree verifies the context-aware acquire still takes an
// uncontended lock promptly.
func TestAcquireContextSucceedsWhenFree(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	guard, err := AcquireContext(ctx, directory, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if guard == nil {
		t.Fatal("exclusive lock was skipped for an existing directory")
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestAcquireContextTimesOutOnHeldLock verifies a blocked acquire honors its context
// deadline instead of waiting for the holder forever.
func TestAcquireContextTimesOutOnHeldLock(t *testing.T) {
	directory := t.TempDir()
	first, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = AcquireContext(ctx, directory, true, io.Discard)
	if err == nil {
		t.Fatal("a held lock was acquired despite the expiry")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a wrapped context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("acquire timed out after %s, want it to stop at the deadline", elapsed)
	}
	// The failed wait must not have taken the lock; the holder still holds it, so a
	// fresh short-deadline acquire must time out too.
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer checkCancel()
	if guard, err := AcquireContext(checkCtx, directory, true, io.Discard); err == nil {
		_ = guard.Release()
		t.Fatal("the timed-out acquire left the lock free while the holder still held it")
	}
}

// TestAcquireContextCancelledBeforeStart verifies a pre-cancelled context fails fast
// even when no other process holds the lock.
func TestAcquireContextCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	guard, err := AcquireContext(ctx, t.TempDir(), true, io.Discard)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want a wrapped context cancellation", err)
	}
	if guard != nil {
		_ = guard.Release()
		t.Fatal("a cancelled acquire returned a guard")
	}
}

// TestAcquireContextSucceedsAfterRelease verifies the retry loop acquires once the
// holder releases, rather than bailing on the first contention.
func TestAcquireContextSucceedsAfterRelease(t *testing.T) {
	directory := t.TempDir()
	first, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	acquired := make(chan error, 1)
	go func() {
		guard, err := AcquireContext(ctx, directory, true, io.Discard)
		if err == nil {
			_ = guard.Release()
		}
		acquired <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("acquire after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not finish after the holder released")
	}
}

func TestAcquireSkipsMissingDirectory(t *testing.T) {
	guard, err := Acquire(filepath.Join(t.TempDir(), "absent"), false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if guard != nil {
		t.Fatal("a missing directory should not be locked")
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRetriesAfterInterruption(t *testing.T) {
	directory := t.TempDir()
	first, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan error, 1)
	go func() {
		guard, err := Acquire(directory, true, io.Discard)
		if err == nil {
			_ = guard.Release()
		}
		acquired <- err
	}()
	// Standard signals coalesce, so space them out: each delivered signal can interrupt the blocking flock with EINTR, which a retry-less implementation would surface as an error.
	for burst := 0; burst < 100; burst++ {
		if err := syscall.Kill(syscall.Getpid(), syscall.SIGWINCH); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("an interrupted wait must retry, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("acquire did not finish after interruptions")
	}
}

func TestAcquireTreatsVanishedDirectoryAsMissing(t *testing.T) {
	// The stat-then-open race inside Acquire is not injectable, so this pins the combined behavior: a directory removed before the call is skipped, not an error.
	directory := t.TempDir()
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	guard, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatalf("a vanished directory must be skipped, not an error: %v", err)
	}
	if guard != nil {
		_ = guard.Release()
		t.Fatal("a missing directory should not be locked")
	}
}

func TestAcquireRejectsRegularFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(path, []byte("shell = \"zsh\""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, true, io.Discard); err == nil {
		t.Fatal("locking a regular file should fail, not silently succeed")
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	directory := t.TempDir()
	guard, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := guard.Release(); err != nil {
		t.Fatalf("second release must be a no-op, got: %v", err)
	}
	// The lock must actually be free again.
	second, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Release()
}

func TestAcquireToleratesNilWarnings(t *testing.T) {
	directory := t.TempDir()
	first, err := Acquire(directory, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan error, 1)
	go func() {
		guard, err := Acquire(directory, true, nil)
		if err == nil {
			_ = guard.Release()
		}
		acquired <- err
	}()
	time.Sleep(20 * time.Millisecond)
	// The second acquire has entered its blocking wait by now, which is where a nil warnings writer used to be dereferenced.
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("nil warnings must not panic or fail: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire with nil warnings did not finish")
	}
}

func TestAcquireWorksOnReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o755) })
	guard, err := Acquire(directory, true, io.Discard)
	if err != nil {
		t.Fatalf("a read-only config directory must still lock: %v", err)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("lock acquisition wrote into a read-only directory: %v", entries)
	}
}

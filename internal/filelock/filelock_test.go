package filelock

import (
	"bytes"
	"io"
	"path/filepath"
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

package autosync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLockPreventsOverlapAndCanBeReacquired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "sync.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %v, %v", info, err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Acquire() = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultLockCoordinatesLowercaseAndLegacyPaths(t *testing.T) {
	root := t.TempDir()
	lock, err := acquireDefault(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, applicationDirectory, "sync.lock"),
		filepath.Join(root, legacyApplicationDirectory(), "sync.lock"),
	} {
		if _, err := Acquire(path); !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("Acquire(%q) = %v, want overlap", path, err)
		}
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLockRejectsSymbolicLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "sync.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(link); err == nil {
		t.Fatal("Acquire() accepted symbolic link")
	}
}

package autosync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrAlreadyRunning means another manual or scheduled synchronization owns the
// process lock.
var ErrAlreadyRunning = errors.New("synchronization is already running")

// Lock is an advisory process lock. Keep the lock file in place across runs so
// competing processes always address the same inode.
type Lock struct {
	file *os.File
}

// AcquireDefault acquires the per-user synchronization lock without waiting.
func AcquireDefault() (*Lock, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve autosync lock directory: %w", err)
	}
	return Acquire(filepath.Join(root, "Overtok", "sync.lock"))
}

// Acquire takes a non-blocking exclusive lock at an explicit path.
func Acquire(path string) (*Lock, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("autosync lock path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create autosync lock directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("secure autosync lock directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("autosync lock must not be a symbolic link")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect autosync lock: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open autosync lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure autosync lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("acquire autosync lock: %w", err)
	}
	return &Lock{file: file}, nil
}

// Close releases the synchronization lock.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	if err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN); err != nil {
		_ = l.file.Close()
		return fmt.Errorf("release autosync lock: %w", err)
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close autosync lock: %w", err)
	}
	l.file = nil
	return nil
}
